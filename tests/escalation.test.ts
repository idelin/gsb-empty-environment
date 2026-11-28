import { VirtualClock } from "../src/clock.js";
import { EscalationEngine, type TargetResolver } from "../src/escalation.js";
import type { EscalationEvent, EscalationPolicy } from "../src/types.js";
import {
  HOUR,
  MINUTE,
  assert,
  assertEqual,
  test,
  utc,
} from "./harness.js";

/** Programmable resolver: maps level id -> users at a given time. */
class ScriptedResolver implements TargetResolver {
  private byLevel = new Map<string, string[] | ((at: number) => string[])>();

  set(levelId: string, users: string[] | ((at: number) => string[])): this {
    this.byLevel.set(levelId, users);
    return this;
  }

  resolve(
    targets: EscalationPolicy["levels"][number]["targets"],
    atMs: number,
  ): string[] {
    // Each test level carries a single user target whose userId equals level id.
    const levelId = targets[0]?.type === "user" ? targets[0].userId : "";
    const value = this.byLevel.get(levelId);
    if (!value) return [];
    return typeof value === "function" ? value(atMs) : value;
  }
}

function policy(
  levelIds: string[],
  delays: number[],
  numLoops: number,
): EscalationPolicy {
  return {
    id: "p",
    numLoops,
    levels: levelIds.map((id, index) => ({
      id,
      delayMinutes: delays[index],
      targets: [{ type: "user", userId: id }],
    })),
  };
}

function types(events: EscalationEvent[]): string[] {
  return events.map((e) => e.type);
}

test("trigger notifies L1 immediately and arms L2 after its delay", () => {
  const clock = new VirtualClock(utc("2026-06-15T10:00:00Z"));
  const resolver = new ScriptedResolver()
    .set("L1", ["alice"])
    .set("L2", ["bob"]);
  const engine = new EscalationEngine({ clock, resolver });
  engine.trigger("a1", policy(["L1", "L2"], [0, 30], 1));

  const alert = engine.getAlert("a1")!;
  assertEqual(alert.state, "triggered", "starts triggered");
  assert(
    types(alert.events).includes("notified"),
    "L1 notified immediately",
  );
  const firstNotify = alert.events.find((e) => e.type === "notified")!;
  assertEqual(firstNotify.levelId, "L1", "first notification is L1");

  // Before the delay nothing new fires.
  engine.advance(clock, 29 * MINUTE);
  assertEqual(
    engine.getAlert("a1")!.events.filter((e) => e.type === "notified").length,
    1,
    "still only L1 notified before delay",
  );

  engine.advance(clock, MINUTE);
  const notifies = engine
    .getAlert("a1")!
    .events.filter((e) => e.type === "notified");
  assertEqual(notifies.length, 2, "L2 fires at the delay boundary");
  assertEqual(notifies[1].levelId, "L2", "second notification is L2");
});

test("empty middle level is skipped immediately without waiting its delay", () => {
  const clock = new VirtualClock(utc("2026-06-15T10:00:00Z"));
  const resolver = new ScriptedResolver()
    .set("L1", ["alice"])
    .set("L2", []) // restricted schedule: nobody on call
    .set("L3", ["carol"]);
  const engine = new EscalationEngine({ clock, resolver });
  engine.trigger("a1", policy(["L1", "L2", "L3"], [0, 60, 60], 1));

  engine.advance(clock, 60 * MINUTE + 1);
  const alert = engine.getAlert("a1")!;
  const skips = alert.events.filter((e) => e.type === "level_skipped_empty");
  assertEqual(skips.length, 1, "one empty-skip event recorded");
  assertEqual(skips[0].levelId, "L2", "L2 marked empty");

  // The engine must NOT idle for L2's 60m. It walks immediately to the first
  // non-empty level (L3) and notifies it at the same instant L2 was evaluated.
  const notified = engine
    .getAlert("a1")!
    .events.filter((e) => e.type === "notified")
    .map((e) => e.levelId);
  assertDeepIncludes(notified, ["L1", "L3"], "skipped L2, reached L3");
  const l3Notify = engine
    .getAlert("a1")!
    .events.find((e) => e.type === "notified" && e.levelId === "L3")!;
  assertEqual(
    l3Notify.atMs,
    utc("2026-06-15T11:00:00Z"),
    "L3 notified immediately, without waiting L2 delay",
  );
  // L2's slot is scheduled, but when it fires the empty target is skipped at
  // once: there is no extra idle dwell and L3 is reached at the same instant.
  const l2Skip = engine
    .getAlert("a1")!
    .events.find((e) => e.type === "level_skipped_empty" && e.levelId === "L2")!;
  assertEqual(
    l2Skip.atMs,
    utc("2026-06-15T11:00:00Z"),
    "empty L2 evaluated exactly at its scheduled time",
  );
  assertEqual(
    engine.getAlert("a1")!.events.filter((e) => e.type === "notified").length,
    2,
    "L1 then immediately L3, no further level",
  );
});

test("all-empty policy terminates as UNASSIGNABLE without looping", () => {
  const clock = new VirtualClock(utc("2026-06-15T10:00:00Z"));
  const resolver = new ScriptedResolver().set("L1", []).set("L2", []);
  const engine = new EscalationEngine({ clock, resolver });
  engine.trigger("a1", policy(["L1", "L2"], [5, 5], 3));

  const alert = engine.getAlert("a1")!;
  assertEqual(alert.state, "unassignable", "terminal unassignable state");
  const skips = alert.events.filter((e) => e.type === "level_skipped_empty");
  // Bounded walk visits each of the (2 levels x 3 loops) slots exactly once.
  assertEqual(skips.length, 6, "every slot probed once then it stops");
  assert(
    types(alert.events).includes("unassignable"),
    "unassignable event emitted",
  );

  // Advancing time further must not produce new events (no infinite loop).
  const before = alert.events.length;
  engine.advance(clock, 24 * HOUR);
  assertEqual(alert.events.length, before, "no events after termination");
});

test("numLoops rewinds to L1 for the configured number of passes", () => {
  const clock = new VirtualClock(utc("2026-06-15T10:00:00Z"));
  const resolver = new ScriptedResolver()
    .set("L1", ["alice"])
    .set("L2", ["bob"]);
  const engine = new EscalationEngine({ clock, resolver });
  engine.trigger("a1", policy(["L1", "L2"], [0, 10], 2));

  // Pass 1: L1 at 10:00, L2 at 10:10; pass 2: L1 at 10:20, L2 at 10:30.
  engine.advance(clock, 40 * MINUTE);
  const loopEvents = engine
    .getAlert("a1")!
    .events.filter((e) => e.type === "notified");
  assertEqual(loopEvents.length, 4, "two levels x two loops = four notifications");
  assertEqual(loopEvents[0].loop, 0, "first pass loop 0");
  assertEqual(loopEvents[2].levelId, "L1", "rewinds to L1");
  assertEqual(loopEvents[2].loop, 1, "second pass loop 1");
  assertEqual(loopEvents[3].levelId, "L2", "finishes on L2 again");

  // No timer remains once loops are exhausted.
  engine.advance(clock, HOUR);
  assertEqual(
    engine.getAlert("a1")!.events.filter((e) => e.type === "notified").length,
    4,
    "no extra notifications beyond numLoops",
  );
});

test("ack cancels the pending escalation timer", () => {
  const clock = new VirtualClock(utc("2026-06-15T10:00:00Z"));
  const resolver = new ScriptedResolver()
    .set("L1", ["alice"])
    .set("L2", ["bob"]);
  const engine = new EscalationEngine({ clock, resolver });
  engine.trigger("a1", policy(["L1", "L2"], [0, 30], 1));

  engine.advance(clock, 10 * MINUTE);
  engine.acknowledge("a1", "alice");
  assertEqual(engine.getAlert("a1")!.state, "acknowledged", "acked state");
  assert(
    types(engine.getAlert("a1")!.events).includes("timer_cancelled"),
    "cancellation event recorded",
  );

  engine.advance(clock, HOUR);
  const notifies = engine
    .getAlert("a1")!
    .events.filter((e) => e.type === "notified");
  assertEqual(notifies.length, 1, "L2 never fires after ack");
});

test("snooze pauses and re-arms escalation timing on resume", () => {
  const clock = new VirtualClock(utc("2026-06-15T10:00:00Z"));
  const resolver = new ScriptedResolver()
    .set("L1", ["alice"])
    .set("L2", ["bob"]);
  const engine = new EscalationEngine({ clock, resolver });
  engine.trigger("a1", policy(["L1", "L2"], [0, 30], 1));

  // Snooze 15 minutes at 10:10, before the 10:30 L2 deadline.
  engine.advance(clock, 10 * MINUTE);
  engine.snooze("a1", 15 * MINUTE);
  assertEqual(engine.getAlert("a1")!.state, "snoozed", "snoozed state");

  // Original 10:30 deadline passes while snoozing: L2 must not fire.
  engine.advance(clock, 20 * MINUTE);
  assertEqual(
    engine.getAlert("a1")!.events.filter((e) => e.type === "notified").length,
    1,
    "no L2 while snoozed",
  );
  assertEqual(engine.getAlert("a1")!.state, "triggered", "resumed to triggered");

  // After resume (10:25) a fresh 30m L2 timer is armed => fires at 10:55.
  const scheduled = engine
    .getAlert("a1")!
    .events.filter((e) => e.type === "escalation_scheduled");
  const lastScheduled = scheduled[scheduled.length - 1];
  assertEqual(
    lastScheduled.fireAtMs,
    utc("2026-06-15T10:55:00Z"),
    "fresh timer measured from resume",
  );

  engine.advance(clock, 30 * MINUTE);
  const notifies = engine
    .getAlert("a1")!
    .events.filter((e) => e.type === "notified")
    .map((e) => e.levelId);
  assertDeepIncludes(notifies, ["L1", "L2"], "L2 fires after resume delay");
});

test("resolve permanently cancels all timers including a pending snooze", () => {
  const clock = new VirtualClock(utc("2026-06-15T10:00:00Z"));
  const resolver = new ScriptedResolver()
    .set("L1", ["alice"])
    .set("L2", ["bob"]);
  const engine = new EscalationEngine({ clock, resolver });
  engine.trigger("a1", policy(["L1", "L2"], [0, 30], 2));

  engine.snooze("a1", 15 * MINUTE);
  engine.resolve("a1", "alice");
  assertEqual(engine.getAlert("a1")!.state, "resolved", "resolved state");

  const before = engine.getAlert("a1")!.events.length;
  engine.advance(clock, 5 * HOUR);
  assertEqual(
    engine.getAlert("a1")!.events.length,
    before,
    "no events at all after resolve",
  );
  assertEqual(
    engine.getAlert("a1")!.events.filter((e) => e.type === "notified").length,
    1,
    "only the initial L1 notification remains",
  );
});

test("terminal alerts reject further lifecycle commands", () => {
  const clock = new VirtualClock(utc("2026-06-15T10:00:00Z"));
  const resolver = new ScriptedResolver().set("L1", ["alice"]);
  const engine = new EscalationEngine({ clock, resolver });
  engine.trigger("a1", policy(["L1"], [0], 1));
  engine.resolve("a1", "alice");
  let threw = false;
  try {
    engine.acknowledge("a1", "alice");
  } catch {
    threw = true;
  }
  assert(threw, "ack after resolve throws");
});

test("re-snooze replaces the pending resume timer and resumes once", () => {
  const clock = new VirtualClock(utc("2026-06-15T10:00:00Z"));
  const resolver = new ScriptedResolver()
    .set("L1", ["alice"])
    .set("L2", ["bob"]);
  const engine = new EscalationEngine({ clock, resolver });
  engine.trigger("a1", policy(["L1", "L2"], [0, 30], 1));

  engine.snooze("a1", 10 * MINUTE);
  engine.advance(clock, 5 * MINUTE);
  // Re-snooze for longer before the first resume fires.
  engine.snooze("a1", 30 * MINUTE);
  assertEqual(engine.getAlert("a1")!.state, "snoozed", "still snoozed");

  // Pass the original (now superseded) 10-minute resume point.
  engine.advance(clock, 10 * MINUTE);
  assertEqual(
    engine.getAlert("a1")!.state,
    "snoozed",
    "old resume timer was replaced, no premature resume",
  );

  // The new 30-minute window completes exactly once.
  engine.advance(clock, 20 * MINUTE);
  assertEqual(
    engine.getAlert("a1")!.state,
    "triggered",
    "resumes once at the re-snoozed deadline",
  );
  const resumes = engine
    .getAlert("a1")!
    .events.filter((e) => e.type === "snooze_resumed");
  assertEqual(resumes.length, 1, "single resume despite two snoozes");
});

test("L1 empty at trigger time is skipped before any timer delay", () => {
  const clock = new VirtualClock(utc("2026-06-15T10:00:00Z"));
  const resolver = new ScriptedResolver().set("L1", []).set("L2", ["bob"]);
  const engine = new EscalationEngine({ clock, resolver });
  engine.trigger("a1", policy(["L1", "L2"], [15, 15], 1));

  const alert = engine.getAlert("a1")!;
  const skip = alert.events.find((e) => e.type === "level_skipped_empty")!;
  assertEqual(skip.atMs, utc("2026-06-15T10:00:00Z"), "L1 evaluated at trigger");
  const notify = alert.events.find((e) => e.type === "notified")!;
  assertEqual(notify.levelId, "L2", "jumped straight to non-empty L2");
  assertEqual(
    notify.atMs,
    utc("2026-06-15T10:00:00Z"),
    "L2 notified at trigger time, no dwell",
  );
});

function assertDeepIncludes(actual: string[], expected: string[], message: string) {
  for (const value of expected) {
    assert(actual.includes(value), `${message}: missing ${value}`);
  }
  assertEqual(actual.length, expected.length, `${message}: length matches`);
}

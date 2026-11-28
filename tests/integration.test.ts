import { VirtualClock } from "../src/clock.js";
import {
  EscalationEngine,
  ScheduleTargetResolver,
} from "../src/escalation.js";
import { whoIsOnCall } from "../src/schedule.js";
import type { EscalationPolicy, Schedule } from "../src/types.js";
import { HOUR, MINUTE, assert, assertEqual, test, utc } from "./harness.js";

const NY = "America/New_York";

test("integration: restricted schedule empties a level and triggers instant skip", () => {
  // Primary team only staffed Mon-Fri 09:00-18:00 local.
  const primary: Schedule = {
    id: "primary",
    layers: [
      {
        id: "primary-layer",
        priority: 1,
        timeZone: NY,
        members: ["oncall-a"],
        unit: "daily",
        handoff: { hour: 9, minute: 0 },
        anchorLocal: { year: 2026, month: 6, day: 15, hour: 9, minute: 0 },
        restrictions: [
          {
            kind: "weekly",
            weekdays: [1, 2, 3, 4, 5],
            start: { hour: 9, minute: 0 },
            end: { hour: 18, minute: 0 },
          },
        ],
      },
    ],
  };

  // Backup team always staffed.
  const backup: Schedule = {
    id: "backup",
    layers: [
      {
        id: "backup-layer",
        priority: 1,
        timeZone: NY,
        members: ["oncall-b"],
        unit: "daily",
        handoff: { hour: 9, minute: 0 },
        anchorLocal: { year: 2026, month: 6, day: 15, hour: 9, minute: 0 },
      },
    ],
  };

  // Saturday 2026-06-20 15:00Z (11:00 local): primary restriction is inactive.
  const saturday = utc("2026-06-20T15:00:00Z");
  assertEqual(whoIsOnCall(primary, saturday), null, "primary empty on Saturday");
  assertEqual(whoIsOnCall(backup, saturday), "oncall-b", "backup always staffed");

  const policy: EscalationPolicy = {
    id: "p",
    numLoops: 1,
    levels: [
      {
        id: "L1",
        delayMinutes: 0,
        targets: [{ type: "schedule", scheduleId: "primary" }],
      },
      {
        id: "L2",
        delayMinutes: 30,
        targets: [{ type: "schedule", scheduleId: "backup" }],
      },
    ],
  };

  const clock = new VirtualClock(saturday);
  const resolver = new ScheduleTargetResolver([primary, backup]);
  const engine = new EscalationEngine({ clock, resolver });
  engine.trigger("alert-1", policy, saturday);

  const alert = engine.getAlert("alert-1")!;
  // L1 empty => skipped instantly, L2 notified immediately (no 30m idle wait).
  assertEqual(alert.state, "triggered", "still active via backup");
  const skip = alert.events.find((e) => e.type === "level_skipped_empty");
  assertEqual(skip?.levelId, "L1", "primary level recorded as empty");
  const notify = alert.events.find((e) => e.type === "notified");
  assertEqual(notify?.levelId, "L2", "backup notified straight away");
  assertDeep(notify?.users ?? [], ["oncall-b"], "backup user resolved");
  assertEqual(
    notify?.atMs,
    saturday,
    "instant skip means notification at trigger time",
  );

  // During business hours the primary schedule is the one that gets notified.
  const monday = utc("2026-06-15T14:00:00Z");
  const clock2 = new VirtualClock(monday);
  const engine2 = new EscalationEngine({
    clock: clock2,
    resolver: new ScheduleTargetResolver([primary, backup]),
  });
  engine2.trigger("alert-2", policy, monday);
  const first = engine2.getAlert("alert-2")!.events.find(
    (e) => e.type === "notified",
  )!;
  assertEqual(first.levelId, "L1", "primary takes L1 on a weekday");
  assertDeep(first.users, ["oncall-a"], "primary user resolved");
  void HOUR;
  void MINUTE;
});

function assertDeep(actual: string[], expected: string[], message: string) {
  assertEqual(actual.length, expected.length, message);
  for (const value of expected) {
    assert(actual.includes(value), `${message}: missing ${value}`);
  }
}

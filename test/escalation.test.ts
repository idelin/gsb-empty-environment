import { test } from "node:test";
import assert from "node:assert/strict";

import {
  EscalationEngine,
  RealClock,
  VirtualClock,
  type EscalationEvent,
  type EscalationPolicy,
  type AlertHandle,
} from "../src/escalation.js";
import type { Schedule } from "../src/schedule.js";
import { HOUR, MINUTE, wall } from "./helpers.js";

const NY = "America/New_York";
const T0 = wall(NY, 2026, 3, 2, 10);

function alwaysCovered(): Schedule {
  return {
    id: "always-on",
    timeZone: NY,
    layers: [
      {
        id: "layer",
        timeZone: NY,
        users: ["oncall-user"],
        start: wall(NY, 2026, 3, 1, 0),
        shiftLength: { days: 7 },
        priority: 1,
      },
    ],
  };
}

function neverCovered(): Schedule {
  return {
    id: "always-off",
    timeZone: NY,
    layers: [
      {
        id: "layer",
        timeZone: NY,
        users: ["ghost"],
        start: wall(NY, 2026, 3, 1, 0),
        shiftLength: { days: 7 },
        priority: 1,
        restrictions: [
          { startWeekday: 6, startHour: 0, durationMinutes: 60 },
        ],
      },
    ],
  };
}

function engine(
  policy: EscalationPolicy,
  schedules: Schedule[],
  clock: VirtualClock,
): EscalationEngine {
  return new EscalationEngine(policy, { schedules, clock });
}

function types(alert: AlertHandle): string[] {
  return alert.events.map((event: EscalationEvent) => event.type);
}

test("L1 notifies immediately and L2 fires only after the configured delay", () => {
  const clock = new VirtualClock(T0);
  const policy: EscalationPolicy = {
    id: "p",
    levels: [
      { delayMinutes: 5, targets: [{ type: "user", user: "u1" }] },
      { delayMinutes: 10, targets: [{ type: "user", user: "u2" }] },
    ],
  };
  const alert = engine(policy, [], clock).triggerAlert("a1");

  assert.deepEqual(types(alert), ["NOTIFY"]);
  assert.deepEqual(alert.events[0], {
    type: "NOTIFY",
    alertId: "a1",
    at: T0,
    levelIndex: 0,
    loopIndex: 0,
    users: ["u1"],
  });

  clock.advance(5 * MINUTE - 1);
  assert.equal(types(alert).filter((type) => type === "NOTIFY").length, 1);

  clock.advance(1);
  assert.equal(types(alert).filter((type) => type === "NOTIFY").length, 2);
  const second = alert.events.find((event) => event.type === "NOTIFY" && event.levelIndex === 1);
  assert.deepEqual(second && "users" in second ? second.users : [], ["u2"]);

  clock.advance(10 * MINUTE);
  assert.ok(types(alert).includes("ESCALATION_EXHAUSTED"));
  assert.equal(clock.pendingCount, 0);
});

test("empty schedule level is skipped instantly without consuming its delay", () => {
  const clock = new VirtualClock(T0);
  const policy: EscalationPolicy = {
    id: "p",
    levels: [
      {
        delayMinutes: 30,
        targets: [{ type: "schedule", scheduleId: "always-off" }],
      },
      { delayMinutes: 7, targets: [{ type: "user", user: "u2" }] },
    ],
  };
  const alert = engine(policy, [neverCovered()], clock).triggerAlert("a2");

  assert.deepEqual(types(alert).slice(0, 2), ["EMPTY_LEVEL_SKIP", "NOTIFY"]);
  assert.equal(alert.events[0].at, T0);
  assert.equal(clock.pendingCount, 1);

  clock.advance(7 * MINUTE);
  assert.ok(types(alert).includes("ESCALATION_EXHAUSTED"));
});

test("all-empty policy immediately becomes UNASSIGNABLE_ALERT and never loops", () => {
  const clock = new VirtualClock(T0);
  const policy: EscalationPolicy = {
    id: "p",
    numLoops: 3,
    levels: [
      {
        delayMinutes: 5,
        targets: [{ type: "schedule", scheduleId: "always-off" }],
      },
      {
        delayMinutes: 5,
        targets: [{ type: "schedule", scheduleId: "missing-schedule" }],
      },
    ],
  };
  const alert = engine(policy, [neverCovered()], clock).triggerAlert("a3");

  assert.equal(alert.state, "unassignable");
  assert.deepEqual(types(alert), [
    "EMPTY_LEVEL_SKIP",
    "EMPTY_LEVEL_SKIP",
    "UNASSIGNABLE_ALERT",
  ]);
  assert.equal(clock.pendingCount, 0);
  clock.runAll();
  assert.equal(types(alert).length, 3);
});

test("numLoops rewinds to L1 for exactly the configured number of passes", () => {
  const clock = new VirtualClock(T0);
  const policy: EscalationPolicy = {
    id: "p",
    numLoops: 2,
    levels: [
      { delayMinutes: 1, targets: [{ type: "user", user: "u1" }] },
      { delayMinutes: 1, targets: [{ type: "user", user: "u2" }] },
    ],
  };
  const alert = engine(policy, [], clock).triggerAlert("a4");
  clock.runAll();

  const notifies = alert.events.filter((event) => event.type === "NOTIFY");
  assert.equal(notifies.length, 6);
  assert.deepEqual(
    notifies.map((event) => ("loopIndex" in event ? event.loopIndex : -1)),
    [0, 0, 1, 1, 2, 2],
  );
  assert.deepEqual(
    notifies.map((event) => ("levelIndex" in event ? event.levelIndex : -1)),
    [0, 1, 0, 1, 0, 1],
  );
  assert.equal(types(alert).filter((type) => type === "ESCALATION_EXHAUSTED").length, 1);
});

test("ack cancels the pending escalation timer permanently", () => {
  const clock = new VirtualClock(T0);
  const policy: EscalationPolicy = {
    id: "p",
    levels: [
      { delayMinutes: 5, targets: [{ type: "user", user: "u1" }] },
      { delayMinutes: 5, targets: [{ type: "user", user: "u2" }] },
    ],
  };
  const eng = engine(policy, [], clock);
  const alert = eng.triggerAlert("a5");

  clock.advance(2 * MINUTE);
  alert.ack("responder");
  assert.equal(alert.state, "acknowledged");
  assert.equal(clock.pendingCount, 0);

  clock.advance(60 * MINUTE);
  assert.deepEqual(types(alert), ["NOTIFY", "ACK"]);
});

test("snooze suspends escalation and restarts the full chain after wake", () => {
  const clock = new VirtualClock(T0);
  const policy: EscalationPolicy = {
    id: "p",
    levels: [
      { delayMinutes: 10, targets: [{ type: "user", user: "u1" }] },
      { delayMinutes: 10, targets: [{ type: "user", user: "u2" }] },
    ],
  };
  const alert = engine(policy, [], clock).triggerAlert("a6");

  clock.advance(5 * MINUTE);
  alert.snooze(15 * MINUTE, "responder");
  assert.equal(alert.state, "snoozed");
  assert.equal(clock.pendingCount, 1);

  clock.advance(15 * MINUTE - 1);
  assert.equal(alert.state, "snoozed");
  clock.advance(1);
  assert.equal(alert.state, "triggered");
  assert.deepEqual(
    types(alert).filter((type) => type === "NOTIFY"),
    ["NOTIFY", "NOTIFY"],
  );
  assert.ok(types(alert).includes("SNOOZE_START"));
  assert.ok(types(alert).includes("SNOOZE_END"));

  clock.advance(10 * MINUTE);
  const levels = alert.events
    .filter((event) => event.type === "NOTIFY")
    .map((event) => ("levelIndex" in event ? event.levelIndex : -1));
  assert.deepEqual(levels, [0, 0, 1]);
});

test("resolve terminates every scheduled timer, including a pending snooze", () => {
  const clock = new VirtualClock(T0);
  const policy: EscalationPolicy = {
    id: "p",
    levels: [{ delayMinutes: 5, targets: [{ type: "user", user: "u1" }] }],
  };
  const alert = engine(policy, [], clock).triggerAlert("a7");

  alert.snooze(30 * MINUTE);
  assert.equal(clock.pendingCount, 1);
  alert.resolve("responder");
  assert.equal(alert.state, "resolved");
  assert.equal(clock.pendingCount, 0);

  clock.advance(2 * HOUR);
  assert.deepEqual(types(alert), ["NOTIFY", "SNOOZE_START", "RESOLVE"]);
});

test("schedule and user targets merge and de-duplicate at the same level", () => {
  const clock = new VirtualClock(T0);
  const policy: EscalationPolicy = {
    id: "p",
    levels: [
      {
        delayMinutes: 5,
        targets: [
          { type: "schedule", scheduleId: "always-on" },
          { type: "user", user: "oncall-user" },
          { type: "user", user: "direct-user" },
        ],
      },
    ],
  };
  const alert = engine(policy, [alwaysCovered()], clock).triggerAlert("a8");
  const notify = alert.events.find((event) => event.type === "NOTIFY");
  assert.deepEqual(notify && "users" in notify ? notify.users : [], [
    "oncall-user",
    "direct-user",
  ]);
});

test("real clock construction is supported for production wiring", () => {
  const eng = new EscalationEngine(
    { id: "p", levels: [{ delayMinutes: 1, targets: [{ type: "user", user: "u" }] }] },
    { clock: new RealClock() },
  );
  const alert = eng.triggerAlert("real");
  assert.equal(alert.state, "triggered");
  alert.resolve();
  assert.equal(alert.state, "resolved");
});

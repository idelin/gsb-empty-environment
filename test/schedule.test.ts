import { test } from "node:test";
import assert from "node:assert/strict";

import {
  buildTimeline,
  coverageTimeline,
  onCallAt,
  onCallUsers,
  type RotationLayer,
  type Schedule,
  type TimelineEntry,
} from "../src/schedule.js";
import { addWallDuration } from "../src/time.js";
import { HOUR, MINUTE, iso, wall } from "./helpers.js";

const NY = "America/New_York";

function dailyLayer(overrides: Partial<RotationLayer> = {}): RotationLayer {
  return {
    id: "layer-primary",
    timeZone: NY,
    users: ["alice", "bob"],
    start: wall(NY, 2026, 3, 2, 9),
    shiftLength: { days: 1 },
    priority: 1,
    ...overrides,
  };
}

function schedule(overrides: Partial<Schedule> = {}): Schedule {
  return {
    id: "sched",
    timeZone: NY,
    layers: [dailyLayer()],
    ...overrides,
  };
}

test("daily rotation alternates users at local 09:00 handoffs", () => {
  const sched = schedule();
  const monday = wall(NY, 2026, 3, 2, 10);
  const tuesday = addWallDuration(NY, monday, { days: 1 });
  const wednesday = addWallDuration(NY, tuesday, { days: 1 });

  assert.equal(onCallAt(sched, monday), "alice");
  assert.equal(onCallAt(sched, tuesday), "bob");
  assert.equal(onCallAt(sched, wednesday), "alice");
});

test("23h spring-forward day still produces exact 09:00 local handoff", () => {
  const sched = schedule();
  const saturday = wall(NY, 2026, 3, 7, 9);
  const sunday = wall(NY, 2026, 3, 8, 9);
  const monday = wall(NY, 2026, 3, 9, 9);

  assert.equal(sunday - saturday, 23 * HOUR);
  assert.equal(onCallAt(sched, saturday), "bob");
  assert.equal(onCallAt(sched, sunday), "alice");
  assert.equal(onCallAt(sched, monday), "bob");

  const timeline = coverageTimeline(sched, saturday, monday);
  assert.deepEqual(
    timeline.map((segment) => [segment.user, segment.end - segment.start]),
    [
      ["bob", 23 * HOUR],
      ["alice", 24 * HOUR],
    ],
  );
});

test("25h fall-back day keeps one continuous shift at local wall handoff", () => {
  const sched = schedule({
    layers: [dailyLayer({ start: wall(NY, 2026, 10, 26, 9) })],
  });
  const saturday = wall(NY, 2026, 10, 31, 9);
  const sunday = wall(NY, 2026, 11, 1, 9);
  const monday = wall(NY, 2026, 11, 2, 9);

  assert.equal(sunday - saturday, 25 * HOUR);
  const timeline = coverageTimeline(sched, saturday, monday);
  assert.equal(timeline.length, 2);
  assert.equal(timeline[0].end - timeline[0].start, 25 * HOUR);
  assert.equal(timeline[1].end - timeline[1].start, 24 * HOUR);
});

test("weekday restriction leaves explicit gaps on the weekend", () => {
  const sched = schedule({
    layers: [
      dailyLayer({
        restrictions: [
          {
            startWeekday: 1,
            startHour: 9,
            durationMinutes: 9 * 60,
          },
        ],
      }),
    ],
  });
  const mondayNoon = wall(NY, 2026, 3, 2, 12);
  const saturdayNoon = wall(NY, 2026, 3, 7, 12);

  assert.equal(onCallAt(sched, mondayNoon), "alice");
  assert.equal(onCallAt(sched, saturdayNoon), null);

  const windowStart = wall(NY, 2026, 3, 6, 0);
  const windowEnd = wall(NY, 2026, 3, 9, 0);
  const timeline = buildTimeline(sched, windowStart, windowEnd);
  const gaps = timeline.filter((entry) => entry.user === null);
  assert.ok(gaps.length >= 1);
  for (const entry of timeline) {
    assert.ok(entry.end > entry.start);
  }
});

test("restriction window crossing midnight is split across two local days", () => {
  const sched = schedule({
    layers: [
      dailyLayer({
        restrictions: [
          {
            startWeekday: 4,
            startHour: 22,
            durationMinutes: 8 * 60,
          },
        ],
      }),
    ],
  });
  const thursdayLate = wall(NY, 2026, 3, 5, 23);
  const fridayEarly = wall(NY, 2026, 3, 6, 5);
  const fridayNoon = wall(NY, 2026, 3, 6, 12);

  assert.equal(onCallAt(sched, thursdayLate), "bob");
  assert.equal(onCallAt(sched, fridayEarly), "bob");
  assert.equal(onCallAt(sched, fridayNoon), null);
});

test("restriction length follows DST: 22:00-06:00 spans 7h on the spring gap day", () => {
  const sched = schedule({
    layers: [
      dailyLayer({
        users: ["alice"],
        restrictions: [
          {
            startWeekday: 6,
            startHour: 22,
            durationMinutes: 8 * 60,
          },
        ],
      }),
    ],
  });
  const saturdayNight = wall(NY, 2026, 3, 7, 22);
  const segments = coverageTimeline(sched, saturdayNight, saturdayNight + 10 * HOUR);
  const total = segments.reduce((sum, segment) => sum + (segment.end - segment.start), 0);
  assert.equal(total, 8 * HOUR);
  assert.equal(segments.length, 1);
  assert.equal(segments[0].start, saturdayNight);
});

test("restriction window gains an hour on the fall-back overlap day", () => {
  const sched = schedule({
    layers: [
      dailyLayer({
        users: ["alice"],
        restrictions: [
          { startWeekday: 0, startHour: 0, durationMinutes: 9 * 60 },
        ],
      }),
    ],
  });
  const overlapMidnight = wall(NY, 2026, 11, 1, 0);
  const segments = coverageTimeline(sched, overlapMidnight, overlapMidnight + 11 * HOUR);
  const total = segments.reduce((sum, segment) => sum + (segment.end - segment.start), 0);
  assert.equal(total, 10 * HOUR);
});

test("higher-priority layer wins while the lower layer keeps filling gaps", () => {
  const low: RotationLayer = {
    id: "layer-low",
    timeZone: NY,
    users: ["backup"],
    start: wall(NY, 2026, 3, 2, 0),
    shiftLength: { days: 7 },
    priority: 1,
  };
  const high: RotationLayer = {
    id: "layer-high",
    timeZone: NY,
    users: ["primary"],
    start: wall(NY, 2026, 3, 2, 9),
    shiftLength: { days: 1 },
    priority: 10,
    restrictions: [
      { startWeekday: 1, startHour: 9, durationMinutes: 9 * 60 },
    ],
  };
  const sched = schedule({ layers: [low, high] });

  const mondayNoon = wall(NY, 2026, 3, 2, 12);
  const mondayNight = wall(NY, 2026, 3, 2, 20);
  assert.equal(onCallAt(sched, mondayNoon), "primary");
  assert.equal(onCallAt(sched, mondayNight), "backup");
});

test("partial override splits one 09:00-18:00 shift into three merged pieces", () => {
  const layer: RotationLayer = {
    id: "layer-9to6",
    timeZone: NY,
    users: ["alice"],
    start: wall(NY, 2026, 3, 2, 9),
    shiftLength: { days: 1 },
    priority: 1,
    restrictions: [
      { startWeekday: 1, startHour: 9, durationMinutes: 9 * 60 },
    ],
  };
  const sched: Schedule = {
    id: "override-split",
    timeZone: NY,
    layers: [layer],
    overrides: [
      {
        id: "ov-afternoon",
        start: wall(NY, 2026, 3, 2, 14),
        end: wall(NY, 2026, 3, 2, 16),
        user: "carol",
      },
    ],
  };

  const start = wall(NY, 2026, 3, 2, 9);
  const end = wall(NY, 2026, 3, 2, 18);
  const timeline = coverageTimeline(sched, start, end);

  assert.deepEqual(
    timeline.map((segment) => [segment.user, segment.source, segment.end - segment.start]),
    [
      ["alice", "layer-9to6", 5 * HOUR],
      ["carol", "ov-afternoon", 2 * HOUR],
      ["alice", "layer-9to6", 2 * HOUR],
    ],
  );

  assert.equal(onCallAt(sched, wall(NY, 2026, 3, 2, 13, 59)), "alice");
  assert.equal(onCallAt(sched, wall(NY, 2026, 3, 2, 14)), "carol");
  assert.equal(onCallAt(sched, wall(NY, 2026, 3, 2, 15, 59)), "carol");
  assert.equal(onCallAt(sched, wall(NY, 2026, 3, 2, 16)), "alice");
});

test("override naming the same user merges back into a single adjacent segment", () => {
  const layer = dailyLayer();
  const sched: Schedule = {
    id: "override-merge",
    timeZone: NY,
    layers: [layer],
    overrides: [
      {
        id: "ov-same-user",
        start: wall(NY, 2026, 3, 2, 14),
        end: wall(NY, 2026, 3, 2, 16),
        user: "alice",
      },
    ],
  };
  const start = wall(NY, 2026, 3, 2, 9);
  const end = wall(NY, 2026, 3, 3, 9);
  const timeline = coverageTimeline(sched, start, end);
  assert.equal(timeline.length, 1);
  assert.equal(timeline[0].user, "alice");
  assert.equal(timeline[0].end - timeline[0].start, 24 * HOUR);
});

test("final UTC timeline is gap-free in coverage and never overlaps", () => {
  const sched = schedule({
    overrides: [
      {
        id: "ov",
        start: wall(NY, 2026, 3, 2, 12),
        end: wall(NY, 2026, 3, 2, 13),
        user: "carol",
      },
    ],
  });
  const start = wall(NY, 2026, 3, 2, 9);
  const end = wall(NY, 2026, 3, 3, 9);
  const entries: TimelineEntry[] = buildTimeline(sched, start, end);

  for (let index = 1; index < entries.length; index += 1) {
    assert.equal(entries[index].start, entries[index - 1].end);
  }
  assert.equal(entries[0].start, start);
  assert.equal(entries[entries.length - 1].end, end);
  assert.equal(iso(entries[0].start), "2026-03-02T14:00:00.000Z");
});

test("onCallUsers aggregates and de-duplicates multiple schedules", () => {
  const first = schedule({ id: "sched-a" });
  const second = schedule({
    id: "sched-b",
    layers: [dailyLayer({ id: "layer-b", users: ["alice", "carol"] })],
  });
  const monday = wall(NY, 2026, 3, 2, 10);
  assert.deepEqual(onCallUsers([first, second], monday), ["alice"]);
});

test("equal-priority overlapping layers resolve deterministically by layer id", () => {
  const first: RotationLayer = {
    id: "layer-a",
    timeZone: NY,
    users: ["user-a"],
    start: wall(NY, 2026, 3, 1, 0),
    shiftLength: { days: 7 },
    priority: 5,
  };
  const second: RotationLayer = {
    id: "layer-b",
    timeZone: NY,
    users: ["user-b"],
    start: wall(NY, 2026, 3, 1, 0),
    shiftLength: { days: 7 },
    priority: 5,
  };
  const sched = schedule({ layers: [second, first] });
  const instant = wall(NY, 2026, 3, 2, 12);
  assert.equal(onCallAt(sched, instant), "user-a");
});

test("timeline rejects an inverted window", () => {
  const start = wall(NY, 2026, 3, 2, 9);
  assert.throws(() => buildTimeline(schedule(), start + MINUTE, start), /end must be strictly after/);
});

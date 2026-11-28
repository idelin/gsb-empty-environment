import { buildTimeline, whoIsOnCall } from "../src/schedule.js";
import type { Override, RotationLayer, Schedule } from "../src/types.js";
import { HOUR, assert, assertEqual, test, utc } from "./harness.js";

const NY = "America/New_York";

function dailyLayer(
  id: string,
  priority: number,
  members: string[],
  anchor: RotationLayer["anchorLocal"],
  restrictions?: RotationLayer["restrictions"],
): RotationLayer {
  return {
    id,
    priority,
    timeZone: NY,
    members,
    unit: "daily",
    handoff: { hour: 9, minute: 0 },
    anchorLocal: anchor,
    restrictions,
  };
}

const monAnchor = { year: 2026, month: 6, day: 15, hour: 9, minute: 0 };

test("daily rotation alternates members at the 09:00 local handoff", () => {
  const layer = dailyLayer("L", 1, ["alice", "bob"], monAnchor);
  const schedule: Schedule = { id: "s", layers: [layer] };
  // 2026-06-15 09:00 EDT = 13:00Z ; next day 09:00 EDT = 13:00Z (summer, 24h).
  assertEqual(
    whoIsOnCall(schedule, utc("2026-06-15T15:00:00Z")),
    "alice",
    "first shift alice",
  );
  assertEqual(
    whoIsOnCall(schedule, utc("2026-06-16T15:00:00Z")),
    "bob",
    "second shift bob",
  );
  assertEqual(
    whoIsOnCall(schedule, utc("2026-06-17T15:00:00Z")),
    "alice",
    "rotation wraps to alice",
  );
});

test("higher-priority layer wins where layers overlap", () => {
  const low = dailyLayer("low", 1, ["alice"], monAnchor);
  const high = dailyLayer("high", 2, ["batman"], monAnchor);
  const schedule: Schedule = { id: "s", layers: [low, high] };
  assertEqual(
    whoIsOnCall(schedule, utc("2026-06-15T15:00:00Z")),
    "batman",
    "high priority layer covers the instant",
  );
});

test("weekday restriction leaves nobody on call outside the window", () => {
  const layer = dailyLayer("L", 1, ["alice"], monAnchor, [
    {
      kind: "weekly",
      weekdays: [1, 2, 3, 4, 5],
      start: { hour: 9, minute: 0 },
      end: { hour: 18, minute: 0 },
    },
  ]);
  const schedule: Schedule = { id: "s", layers: [layer] };
  // Monday 13:00Z (09:00 local) inside; Saturday and 08:00 local outside.
  assertEqual(
    whoIsOnCall(schedule, utc("2026-06-15T13:30:00Z")),
    "alice",
    "weekday inside window",
  );
  assertEqual(
    whoIsOnCall(schedule, utc("2026-06-20T15:00:00Z")),
    null,
    "Saturday has no coverage",
  );
  assertEqual(
    whoIsOnCall(schedule, utc("2026-06-15T12:30:00Z")),
    null,
    "08:30 local before window",
  );
});

test("restricted timeline only contains the 09:00-18:00 local window", () => {
  const layer = dailyLayer("L", 1, ["alice"], monAnchor, [
    {
      kind: "weekly",
      weekdays: [1],
      start: { hour: 9, minute: 0 },
      end: { hour: 18, minute: 0 },
    },
  ]);
  const timeline = buildTimeline(
    { id: "s", layers: [layer] },
    utc("2026-06-15T00:00:00Z"),
    utc("2026-06-16T00:00:00Z"),
  );
  assertEqual(timeline.length, 1, "one covered segment");
  assertEqual(
    timeline[0].startMs,
    utc("2026-06-15T13:00:00Z"),
    "window opens 09:00 EDT",
  );
  assertEqual(
    timeline[0].endMs,
    utc("2026-06-15T22:00:00Z"),
    "window closes 18:00 EDT",
  );
});

test("cross-midnight restriction is split into two UTC pieces", () => {
  const layer = dailyLayer("L", 1, ["alice"], monAnchor, [
    {
      kind: "weekly",
      weekdays: [1], // Monday 22:00 -> Tuesday 06:00
      start: { hour: 22, minute: 0 },
      end: { hour: 6, minute: 0 },
    },
  ]);
  const timeline = buildTimeline(
    { id: "s", layers: [layer] },
    utc("2026-06-15T00:00:00Z"),
    utc("2026-06-16T12:00:00Z"),
  );
  // Evening piece Mon 22:00-24:00 EDT (02:00-04:00Z Tue) and morning piece
  // Tue 00:00-06:00 EDT (04:00-10:00Z). They abut at 04:00Z but are the same
  // user, so they coalesce into one continuous segment.
  assert(timeline.length >= 1, "at least one segment produced");
  const first = timeline[0];
  assertEqual(
    first.startMs,
    utc("2026-06-16T02:00:00Z").valueOf(),
    "evening start 22:00 EDT = 02:00Z",
  );
  const last = timeline[timeline.length - 1];
  assertEqual(
    last.endMs,
    utc("2026-06-16T10:00:00Z"),
    "morning end 06:00 EDT = 10:00Z",
  );
  // Prove the split is real (not a naive same-day interval): 23:00 local is
  // covered and 12:00 local Tuesday is not.
  assertEqual(
    whoIsOnCall({ id: "s", layers: [layer] }, utc("2026-06-16T03:30:00Z")),
    "alice",
    "23:30 local covered",
  );
  assertEqual(
    whoIsOnCall({ id: "s", layers: [layer] }, utc("2026-06-16T16:00:00Z")),
    null,
    "noon Tuesday not covered",
  );
});

test("partial override splits a 09:00-18:00 shift into exactly three segments", () => {
  const layer = dailyLayer("L", 1, ["alice"], monAnchor, [
    {
      kind: "weekly",
      weekdays: [1, 2, 3, 4, 5],
      start: { hour: 9, minute: 0 },
      end: { hour: 18, minute: 0 },
    },
  ]);
  const override: Override = {
    id: "ov",
    userId: "carol",
    // 14:00-16:00 local EDT = 18:00Z-20:00Z.
    startMs: utc("2026-06-15T18:00:00Z"),
    endMs: utc("2026-06-15T20:00:00Z"),
  };
  const timeline = buildTimeline(
    { id: "s", layers: [layer], overrides: [override] },
    utc("2026-06-15T13:00:00Z"),
    utc("2026-06-15T22:00:00Z"),
  );
  assertEqual(timeline.length, 3, "override splits coverage into three");
  assertEqual(timeline[0].userId, "alice", "before override alice");
  assertEqual(timeline[1].userId, "carol", "override window carol");
  assertEqual(timeline[2].userId, "alice", "after override alice");
  assertEqual(timeline[1].startMs, utc("2026-06-15T18:00:00Z"), "split start");
  assertEqual(timeline[1].endMs, utc("2026-06-15T20:00:00Z"), "split end");
  assertEqual(timeline[0].source.type, "layer", "first segment from layer");
  assertEqual(timeline[1].source.type, "override", "middle segment from override");
});

test("override by the same user merges adjacent segments back together", () => {
  const layer = dailyLayer("L", 1, ["alice"], monAnchor, [
    {
      kind: "weekly",
      weekdays: [1, 2, 3, 4, 5],
      start: { hour: 9, minute: 0 },
      end: { hour: 18, minute: 0 },
    },
  ]);
  const override: Override = {
    id: "ov-same",
    userId: "alice",
    startMs: utc("2026-06-15T18:00:00Z"),
    endMs: utc("2026-06-15T20:00:00Z"),
  };
  const timeline = buildTimeline(
    { id: "s", layers: [layer], overrides: [override] },
    utc("2026-06-15T13:00:00Z"),
    utc("2026-06-15T22:00:00Z"),
  );
  assertEqual(timeline.length, 1, "adjacent same-user pieces coalesce");
  assertEqual(timeline[0].userId, "alice", "single alice segment");
  assertEqual(timeline[0].startMs, utc("2026-06-15T13:00:00Z"), "full start");
  assertEqual(timeline[0].endMs, utc("2026-06-15T22:00:00Z"), "full end");
});

test("override always beats a higher-priority layer", () => {
  const low = dailyLayer("low", 1, ["alice"], monAnchor);
  const high = dailyLayer("high", 2, ["batman"], monAnchor);
  const override: Override = {
    id: "ov",
    userId: "carol",
    startMs: utc("2026-06-15T18:00:00Z"),
    endMs: utc("2026-06-15T20:00:00Z"),
  };
  const schedule: Schedule = {
    id: "s",
    layers: [low, high],
    overrides: [override],
  };
  assertEqual(
    whoIsOnCall(schedule, utc("2026-06-15T19:00:00Z")),
    "carol",
    "override wins over both layers",
  );
  assertEqual(
    whoIsOnCall(schedule, utc("2026-06-15T15:00:00Z")),
    "batman",
    "high layer wins outside override",
  );
});

test("timeline is non-overlapping, half-open and fully ordered", () => {
  const layer = dailyLayer("L", 1, ["alice", "bob"], monAnchor);
  const timeline = buildTimeline(
    { id: "s", layers: [layer] },
    utc("2026-06-15T13:00:00Z"),
    utc("2026-06-17T13:00:00Z"),
  );
  for (let i = 1; i < timeline.length; i += 1) {
    assert(
      timeline[i].startMs >= timeline[i - 1].endMs,
      "segments never overlap and are ordered",
    );
  }
  void HOUR;
});

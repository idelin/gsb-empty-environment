import { buildTimeline } from "../src/schedule.js";
import type { RotationLayer, Schedule } from "../src/types.js";
import { HOUR, assert, assertEqual, test, utc } from "./harness.js";

const NY = "America/New_York";

test("rotation timeline handoffs track 23h/24h/25h across the DST weekend", () => {
  // Anchor at 09:00 local on the Thursday before the spring-forward (Mar 5).
  const layer: RotationLayer = {
    id: "dst-layer",
    priority: 1,
    timeZone: NY,
    members: ["a", "b"],
    unit: "daily",
    handoff: { hour: 9, minute: 0 },
    anchorLocal: { year: 2026, month: 3, day: 5, hour: 9, minute: 0 },
  };
  const schedule: Schedule = { id: "s", layers: [layer] };

  const timeline = buildTimeline(
    schedule,
    utc("2026-03-05T14:00:00Z"),
    utc("2026-03-10T13:00:00Z"),
  );

  // Every handoff stays 09:00 local; derive each segment's UTC duration.
  const durations = timeline.map((segment) => segment.endMs - segment.startMs);
  assertEqual(durations.length, 5, "five daily shifts");
  // Mar5(EST->EST)=24h, Mar6=24h, Mar7->Mar8 spans the gap=23h, then 24h.
  assertEqual(durations[0], 24 * HOUR, "Mar 5 shift 24h");
  assertEqual(durations[1], 24 * HOUR, "Mar 6 shift 24h");
  assertEqual(durations[2], 23 * HOUR, "Mar 7 -> Mar 8 spring shift 23h");
  assertEqual(durations[3], 24 * HOUR, "Mar 8 shift 24h (EDT)");
  assertEqual(durations[4], 24 * HOUR, "Mar 9 shift 24h");

  // Members alternate regardless of the shortened day.
  assertDeep(
    timeline.map((segment) => segment.userId),
    ["a", "b", "a", "b", "a"],
    "members alternate across the DST boundary",
  );

  // The 23h segment's boundaries: Mar 7 09:00 EST (14:00Z) -> Mar 8 09:00 EDT.
  assertEqual(
    timeline[2].startMs,
    utc("2026-03-07T14:00:00Z"),
    "gap-eve handoff at 14:00Z",
  );
  assertEqual(
    timeline[2].endMs,
    utc("2026-03-08T13:00:00Z"),
    "gap-day handoff at 13:00Z",
  );

  // No overlaps and contiguous coverage.
  for (let i = 1; i < timeline.length; i += 1) {
    assert(
      timeline[i].startMs === timeline[i - 1].endMs,
      "segments abut with no gap or overlap",
    );
  }
});

test("fall-back weekend produces a 25h shift in the rendered timeline", () => {
  const layer: RotationLayer = {
    id: "fall-layer",
    priority: 1,
    timeZone: NY,
    members: ["a"],
    unit: "daily",
    handoff: { hour: 9, minute: 0 },
    anchorLocal: { year: 2026, month: 10, day: 31, hour: 9, minute: 0 },
  };
  const timeline = buildTimeline(
    { id: "s", layers: [layer] },
    utc("2026-10-31T13:00:00Z"),
    utc("2026-11-01T14:00:00Z"),
  );
  assertEqual(timeline.length, 1, "single user means one coalesced segment");
  assertEqual(
    timeline[0].endMs - timeline[0].startMs,
    25 * HOUR,
    "fall-back day lasts 25h",
  );
});

function assertDeep(actual: string[], expected: string[], message: string) {
  assertEqual(actual.length, expected.length, message);
  for (let i = 0; i < expected.length; i += 1) {
    assertEqual(actual[i], expected[i], `${message} [${i}]`);
  }
}

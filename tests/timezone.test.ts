import {
  advanceWallParts,
  offsetMsAt,
  utcToZonedParts,
  zonedPartsToUtc,
  zonedTimeToUtc,
} from "../src/timezone.js";
import { HOUR, MINUTE, assertEqual, test, utc } from "./harness.js";

const NY = "America/New_York";
const BERLIN = "Europe/Berlin";

test("NY offset flips between EST (-5h) and EDT (-4h)", () => {
  assertEqual(
    offsetMsAt(NY, utc("2026-01-15T14:00:00Z")),
    -5 * HOUR,
    "winter EST offset",
  );
  assertEqual(
    offsetMsAt(NY, utc("2026-07-15T14:00:00Z")),
    -4 * HOUR,
    "summer EDT offset",
  );
});

test("daily 09:00 NY handoff spans 23h across the spring-forward gap", () => {
  // 2026-03-08 is the NY spring-forward day (02:00 -> 03:00). The 23h shift is
  // the day ending on the transition: Mar 7 09:00 EST (14:00Z) -> Mar 8 09:00
  // EDT (13:00Z).
  const before = zonedTimeToUtc(
    NY,
    { year: 2026, month: 3, day: 7 },
    { hour: 9, minute: 0 },
  );
  const after = advanceWallParts(NY, before, 1, "day");
  assertEqual(after - before, 23 * HOUR, "spring day shift must be 23h");

  // Both endpoints remain 09:00 local wall time.
  const p0 = utcToZonedParts(NY, before);
  const p1 = utcToZonedParts(NY, after);
  assertEqual(p0.hour, 9, "before handoff local hour");
  assertEqual(p1.hour, 9, "after handoff local hour");
});

test("daily 09:00 NY handoff spans 25h across the fall-back overlap", () => {
  // 2026-11-01 is the NY fall-back day. The 25h shift is Oct 31 09:00 EDT
  // (13:00Z) -> Nov 1 09:00 EST (14:00Z).
  const before = zonedTimeToUtc(
    NY,
    { year: 2026, month: 10, day: 31 },
    { hour: 9, minute: 0 },
  );
  const after = advanceWallParts(NY, before, 1, "day");
  assertEqual(after - before, 25 * HOUR, "fall day shift must be 25h");

  const p1 = utcToZonedParts(NY, after);
  assertEqual(p1.hour, 9, "after handoff stays 09:00 local");
});

test("a normal NY day still spans exactly 24h", () => {
  const before = zonedTimeToUtc(
    NY,
    { year: 2026, month: 6, day: 15 },
    { hour: 9, minute: 0 },
  );
  const after = advanceWallParts(NY, before, 1, "day");
  assertEqual(after - before, 24 * HOUR, "ordinary day is 24h");
});

test("Europe/Berlin spring-forward produces a 23h local-day step", () => {
  // 2026-03-29 02:00 -> 03:00 in Berlin; 23h from Mar 28 to Mar 29 09:00.
  const before = zonedTimeToUtc(
    BERLIN,
    { year: 2026, month: 3, day: 28 },
    { hour: 9, minute: 0 },
  );
  const after = advanceWallParts(BERLIN, before, 1, "day");
  assertEqual(after - before, 23 * HOUR, "Berlin spring day is 23h");
});

test("Europe/Berlin fall-back produces a 25h local-day step", () => {
  // 2026-10-25 03:00 -> 02:00 in Berlin; 25h from Oct 24 to Oct 25 09:00.
  const before = zonedTimeToUtc(
    BERLIN,
    { year: 2026, month: 10, day: 24 },
    { hour: 9, minute: 0 },
  );
  const after = advanceWallParts(BERLIN, before, 1, "day");
  assertEqual(after - before, 25 * HOUR, "Berlin fall day is 25h");
});

test("spring-forward gap wall time jumps forward to first valid instant", () => {
  // 02:30 on 2026-03-08 does not exist in NY. It must resolve forward to 03:30
  // EDT = 07:30Z.
  const instant = zonedPartsToUtc(NY, {
    year: 2026,
    month: 3,
    day: 8,
    hour: 2,
    minute: 30,
  });
  assertEqual(instant, utc("2026-03-08T07:30:00Z"), "gap shifts forward");
  const parts = utcToZonedParts(NY, instant);
  assertEqual(parts.hour, 3, "resolved local hour is 03");
  assertEqual(parts.minute, 30, "resolved local minute is 30");
});

test("fall-back overlap resolves to earlier then later instant on request", () => {
  // 01:30 exists twice on 2026-11-01: EDT (05:30Z) then EST (06:30Z).
  const parts = { year: 2026, month: 11, day: 1, hour: 1, minute: 30 };
  const earlier = zonedPartsToUtc(NY, parts);
  const later = zonedPartsToUtc(NY, parts, { disambiguation: "later" });
  assertEqual(earlier, utc("2026-11-01T05:30:00Z"), "earlier occurrence EDT");
  assertEqual(later, utc("2026-11-01T06:30:00Z"), "later occurrence EST");
  assertEqual(later - earlier, HOUR, "overlap occurrences are one hour apart");
});

test("weekly stepping stays on the same weekday across several weeks", () => {
  const start = zonedTimeToUtc(
    NY,
    { year: 2026, month: 3, day: 2 },
    { hour: 9, minute: 0 },
  ); // Monday 2026-03-02
  const fourWeeks = advanceWallParts(NY, start, 4, "week");
  const parts = utcToZonedParts(NY, fourWeeks);
  assertEqual(parts.day, 30, "four Mondays later is March 30");
  assertEqual(parts.hour, 9, "handoff time preserved");
});

test("custom duration is a fixed elapsed amount with no day assumption", () => {
  const start = utc("2026-03-08T14:00:00Z");
  const next = advanceWallParts(NY, start, 1, "custom", 12 * HOUR + 30 * MINUTE);
  assertEqual(
    next - start,
    12 * HOUR + 30 * MINUTE,
    "custom shift is exactly the configured duration",
  );
});

const assert = {
  strictEqual(actual: unknown, expected: unknown, message?: string): void {
    if (!Object.is(actual, expected)) {
      throw new Error(
        `assert.strictEqual failed: ${String(actual)} !== ${String(expected)}` +
          (message ? ` — ${message}` : ""),
      );
    }
  },
  deepStrictEqual(actual: unknown, expected: unknown, message?: string): void {
    if (JSON.stringify(actual) !== JSON.stringify(expected)) {
      throw new Error(
        `assert.deepStrictEqual failed: ${JSON.stringify(actual)} !== ${JSON.stringify(expected)}` +
          (message ? ` — ${message}` : ""),
      );
    }
  },
  ok(value: unknown, message?: string): void {
    if (!value) throw new Error(`assert.ok failed` + (message ? ` — ${message}` : ""));
  },
  throws(fn: () => unknown, errorClass?: new (...args: never[]) => Error, message?: string): void {
    try {
      fn();
    } catch (err) {
      if (errorClass && !(err instanceof errorClass)) {
        throw new Error(
          `assert.throws: expected ${errorClass.name}, got ${(err as Error).constructor.name}`,
        );
      }
      return;
    }
    throw new Error(`assert.throws: expected ${errorClass?.name ?? "an error"}` + (message ? ` — ${message}` : ""));
  },
};
import {
  AmbiguousLocalTimeError,
  NonExistentLocalTimeError,
  NonPositiveIntervalError,
  classifyLocal,
  createZoneTable,
  findGap,
  localIntervalToUtc,
  localToUtcCandidates,
  offsetAtUtc,
  resolveLocal,
  utcToLocal,
  type ZoneTable,
} from "../src/index";

const MIN = 60_000;
const HOUR = 60 * MIN;

// ---- Zones under test (all rules explicit; no system timezone involved) ----

// Spring forward: +01:00 -> +02:00, one-hour gap.
const FWD = Date.UTC(2026, 2, 29, 1, 0, 0);
const springZone: ZoneTable = createZoneTable({
  name: "Test/SpringForward",
  initialOffsetMinutes: 60,
  transitions: [{ utc: FWD, offsetMinutes: 120 }],
});

// Fall back: +02:00 -> +01:00, one-hour fold.
const BACK = Date.UTC(2026, 9, 25, 1, 0, 0);
const fallZone: ZoneTable = createZoneTable({
  name: "Test/FallBack",
  initialOffsetMinutes: 120,
  transitions: [{ utc: BACK, offsetMinutes: 60 }],
});

// Lord Howe style: +10:30 <-> +11:00, half-hour gap then half-hour fold.
const H1 = Date.UTC(2026, 9, 4, 16, 0, 0); // local 02:30 -> 03:00? (02:00 +10:30)
const H2 = Date.UTC(2027, 3, 4, 15, 30, 0);
const halfHourZone: ZoneTable = createZoneTable({
  name: "Test/HalfHour",
  initialOffsetMinutes: 630,
  transitions: [
    { utc: H1, offsetMinutes: 660 },
    { utc: H2, offsetMinutes: 630 },
  ],
});

// Kathmandu style: +05:30 -> +05:45, fifteen-minute gap; offsets never whole hours.
const Q1 = Date.UTC(2026, 0, 1, 0, 0, 0);
const quarterZone: ZoneTable = createZoneTable({
  name: "Test/QuarterHour",
  initialOffsetMinutes: 330,
  transitions: [{ utc: Q1, offsetMinutes: 345 }],
});

// ---- UTC-mapping enumeration ------------------------------------------------
// For every UTC instant in a window, map to local and back; the forward
// instant must appear among the candidates, and every candidate must map
// back to the same local reading. Local-minute enumeration then verifies
// that gap/fold sizes match the transition shifts exactly.

function enumerateUtcMapping(zone: ZoneTable, fromUtc: number, toUtc: number): void {
  for (let utc = fromUtc; utc <= toUtc; utc += MIN) {
    const { local, offsetMinutes } = utcToLocal(zone, utc);
    assert.strictEqual(offsetMinutes, offsetAtUtc(zone, utc), "offset lookup agrees");
    const candidates = localToUtcCandidates(zone, local);
    assert.ok(
      candidates.includes(utc),
      `${zone.name}: utc ${utc} (local ${local}) missing from candidates [${candidates}]`,
    );
    for (const c of candidates) {
      assert.strictEqual(
        utcToLocal(zone, c).local,
        local,
        `${zone.name}: candidate ${c} must map back to local ${local}`,
      );
    }
  }
}

function countLocalOutcomes(
  zone: ZoneTable,
  fromLocal: number,
  toLocal: number,
): { none: number; single: number; ambiguous: number } {
  let none = 0;
  let single = 0;
  let ambiguous = 0;
  for (let local = fromLocal; local < toLocal; local += MIN) {
    const kind = classifyLocal(zone, local).kind;
    if (kind === "none") none++;
    else if (kind === "single") single++;
    else ambiguous++;
  }
  return { none, single, ambiguous };
}

// Spring forward: exactly 60 local minutes vanish, none repeat.
{
  enumerateUtcMapping(springZone, FWD - 2 * HOUR, FWD + 2 * HOUR);
  const counts = countLocalOutcomes(springZone, FWD, FWD + 3 * HOUR);
  assert.strictEqual(counts.none, 60, "spring forward removes exactly 60 local minutes");
  assert.strictEqual(counts.ambiguous, 0, "spring forward creates no fold");
}

// Fall back: exactly 60 local minutes repeat, none vanish.
{
  enumerateUtcMapping(fallZone, BACK - 2 * HOUR, BACK + 2 * HOUR);
  const counts = countLocalOutcomes(fallZone, BACK, BACK + 3 * HOUR);
  assert.strictEqual(counts.ambiguous, 60, "fall back repeats exactly 60 local minutes");
  assert.strictEqual(counts.none, 0, "fall back creates no gap");
  // Fold candidates are exactly one hour apart.
  const foldLocal = BACK + 90 * MIN;
  const [earlier, later] = localToUtcCandidates(fallZone, foldLocal);
  assert.strictEqual(later! - earlier!, HOUR, "fold candidates differ by the shift");
}

// Half-hour shifts with non-integer-hour offsets (+10:30 / +11:00).
{
  enumerateUtcMapping(halfHourZone, H1 - 2 * HOUR, H2 + 2 * HOUR);
  const gapCounts = countLocalOutcomes(halfHourZone, H1 + 10 * HOUR, H1 + 13 * HOUR);
  assert.strictEqual(gapCounts.none, 30, "half-hour spring forward removes 30 minutes");
  const foldCounts = countLocalOutcomes(halfHourZone, H2 + 10 * HOUR, H2 + 13 * HOUR);
  assert.strictEqual(foldCounts.ambiguous, 30, "half-hour fall back repeats 30 minutes");
  const [earlier, later] = localToUtcCandidates(halfHourZone, H2 + 645 * MIN);
  assert.strictEqual(later! - earlier!, 30 * MIN, "half-hour fold spacing");
}

// Quarter-hour shift between two non-integer-hour offsets (+05:30 / +05:45).
{
  enumerateUtcMapping(quarterZone, Q1 - HOUR, Q1 + HOUR);
  const counts = countLocalOutcomes(quarterZone, Q1 + 5 * HOUR, Q1 + 7 * HOUR);
  assert.strictEqual(counts.none, 15, "quarter-hour jump removes 15 minutes");
  assert.strictEqual(counts.ambiguous, 0);
  // +05:45 maps utc 0 to local 345 minutes past the epoch.
  const fixed: ZoneTable = createZoneTable({
    name: "Test/FixedKathmandu",
    initialOffsetMinutes: 345,
    transitions: [],
  });
  assert.strictEqual(utcToLocal(fixed, 0).local, 345 * MIN);
  assert.deepStrictEqual(localToUtcCandidates(fixed, 345 * MIN), [0]);
  assert.strictEqual(resolveLocal(fixed, 345 * MIN), 0);
}

// ---- Gap / fold API behaviour ----------------------------------------------

// Gap local time: reported nonexistent, resolution always throws.
{
  const gapLocal = FWD + 90 * MIN; // 03:30 local, skipped by the 02:00->03:00 jump
  assert.strictEqual(classifyLocal(springZone, gapLocal).kind, "none");
  const gap = findGap(springZone, gapLocal);
  assert.strictEqual(gap.gapStartLocal, FWD + 60 * MIN);
  assert.strictEqual(gap.gapEndLocal, FWD + 120 * MIN);
  assert.throws(() => resolveLocal(springZone, gapLocal), NonExistentLocalTimeError);
  assert.throws(
    () => resolveLocal(springZone, gapLocal, "earlier"),
    NonExistentLocalTimeError,
    "fold choice cannot rescue a nonexistent time",
  );
}

// Fold local time: two candidates, explicit choice required.
{
  const foldLocal = BACK + 90 * MIN; // 02:30 local occurs twice
  const resolution = classifyLocal(fallZone, foldLocal);
  assert.strictEqual(resolution.kind, "ambiguous");
  assert.throws(() => resolveLocal(fallZone, foldLocal), AmbiguousLocalTimeError);
  assert.strictEqual(resolveLocal(fallZone, foldLocal, "earlier"), BACK - 30 * MIN);
  assert.strictEqual(resolveLocal(fallZone, foldLocal, "later"), BACK + 30 * MIN);
}

// Ordinary local time: single candidate, choice is irrelevant.
{
  const plain = FWD - 30 * MIN;
  assert.strictEqual(classifyLocal(springZone, plain).kind, "single");
  assert.strictEqual(resolveLocal(springZone, plain), plain - 60 * MIN);
  assert.strictEqual(resolveLocal(springZone, plain, "later"), plain - 60 * MIN);
}

// ---- Interval validation ----------------------------------------------------

// Interval across the spring-forward gap: wall duration minus the lost hour.
{
  const interval = localIntervalToUtc(springZone, FWD + 30 * MIN, FWD + 150 * MIN);
  assert.strictEqual(interval.durationMs, HOUR, "2h wall clock across a 1h gap is 1h");
}

// Interval inside the fall-back fold: endpoints disambiguated independently.
{
  const interval = localIntervalToUtc(fallZone, BACK + 60 * MIN, BACK + 120 * MIN, {
    startFoldChoice: "earlier",
    endFoldChoice: "later",
  });
  assert.strictEqual(interval.durationMs, 2 * HOUR, "1h wall clock across a 1h fold is 2h");
}

// Endpoints whose chosen UTC instants are out of order are rejected.
{
  assert.throws(
    () =>
      localIntervalToUtc(fallZone, BACK + 90 * MIN, BACK + 100 * MIN, {
        startFoldChoice: "later", // BACK + 30min
        endFoldChoice: "earlier", // BACK - 20min
      }),
    NonPositiveIntervalError,
  );
  assert.throws(
    () => localIntervalToUtc(springZone, FWD - 10 * MIN, FWD - 10 * MIN),
    NonPositiveIntervalError,
    "zero-length interval is rejected",
  );
}

console.log("All enumeration and API checks passed.");

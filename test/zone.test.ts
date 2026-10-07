import { describe, expect, test } from 'bun:test';
import {
  AmbiguousLocalTimeError,
  civilMs,
  enumerateUtcMapping,
  InvalidTransitionTableError,
  localToUtc,
  NonexistentLocalTimeError,
  NonPositiveIntervalError,
  resolveInterval,
  resolveLocal,
  TransitionTable,
  utcToLocal,
} from '../src/index';

const MIN = 60_000;
const HOUR = 3_600_000;

// US-Eastern-like: spring forward 1h (gap), fall back 1h (fold).
const SPRING_UTC = Date.UTC(2024, 2, 10, 7, 0, 0); // 02:00 EST -> 03:00 EDT
const FALL_UTC = Date.UTC(2024, 10, 3, 6, 0, 0); //   02:00 EDT -> 01:00 EST
const eastern = new TransitionTable({
  initialOffsetMinutes: -300, // UTC-5
  transitions: [
    { utcMs: SPRING_UTC, offsetMinutes: -240 }, // UTC-4
    { utcMs: FALL_UTC, offsetMinutes: -300 },
  ],
});

// Nepal-like non-integer offsets: +5:45 -> +5:30 (15-min fold), then back (15-min gap).
const NP_FOLD_UTC = Date.UTC(2024, 0, 15, 0, 0, 0);
const NP_GAP_UTC = Date.UTC(2024, 6, 15, 0, 0, 0);
const nepal = new TransitionTable({
  initialOffsetMinutes: 345, // UTC+5:45
  transitions: [
    { utcMs: NP_FOLD_UTC, offsetMinutes: 330 }, // UTC+5:30
    { utcMs: NP_GAP_UTC, offsetMinutes: 345 },
  ],
});

// Lord Howe-like: 30-minute DST shifts between +10:30 and +11:00.
const LH_FWD_UTC = Date.UTC(2024, 9, 6, 16, 0, 0);
const LH_BACK_UTC = Date.UTC(2025, 3, 6, 15, 30, 0);
const lordHowe = new TransitionTable({
  initialOffsetMinutes: 630, // UTC+10:30
  transitions: [
    { utcMs: LH_FWD_UTC, offsetMinutes: 660 }, // UTC+11:00
    { utcMs: LH_BACK_UTC, offsetMinutes: 630 },
  ],
});

describe('table validation', () => {
  test('rejects unsorted or duplicate transitions', () => {
    expect(
      () =>
        new TransitionTable({
          initialOffsetMinutes: 0,
          transitions: [
            { utcMs: 2_000, offsetMinutes: 60 },
            { utcMs: 1_000, offsetMinutes: 0 },
          ],
        }),
    ).toThrow(InvalidTransitionTableError);
    expect(
      () =>
        new TransitionTable({
          initialOffsetMinutes: 0,
          transitions: [
            { utcMs: 1_000, offsetMinutes: 60 },
            { utcMs: 1_000, offsetMinutes: 0 },
          ],
        }),
    ).toThrow(InvalidTransitionTableError);
  });

  test('rejects non-integer and out-of-range offsets', () => {
    expect(() => new TransitionTable({ initialOffsetMinutes: 5.5, transitions: [] })).toThrow(
      InvalidTransitionTableError,
    );
    expect(
      () =>
        new TransitionTable({
          initialOffsetMinutes: 0,
          transitions: [{ utcMs: 0, offsetMinutes: 24 * 60 + 1 }],
        }),
    ).toThrow(InvalidTransitionTableError);
  });
});

describe('spring forward (gap)', () => {
  const gapStartLocal = SPRING_UTC - 300 * MIN; // 02:00 local, first missing instant
  const gapEndLocal = SPRING_UTC - 240 * MIN; //   03:00 local, first valid instant

  test('local times inside the gap do not exist', () => {
    expect(localToUtc(eastern, gapStartLocal)).toEqual({ kind: 'none' });
    expect(localToUtc(eastern, gapStartLocal + 30 * MIN)).toEqual({ kind: 'none' });
    expect(localToUtc(eastern, gapEndLocal - 1)).toEqual({ kind: 'none' });
  });

  test('gap boundaries map to the transition instant', () => {
    expect(localToUtc(eastern, gapStartLocal - 1)).toEqual({ kind: 'unique', utcMs: SPRING_UTC - 1 });
    expect(localToUtc(eastern, gapEndLocal)).toEqual({ kind: 'unique', utcMs: SPRING_UTC });
  });

  test('resolveLocal throws NonexistentLocalTimeError in the gap', () => {
    expect(() => resolveLocal(eastern, gapStartLocal + 10 * MIN)).toThrow(NonexistentLocalTimeError);
  });
});

describe('fall back (fold)', () => {
  const foldStartLocal = FALL_UTC - 300 * MIN; // 01:00 local, first repeated instant
  const foldEndLocal = FALL_UTC - 240 * MIN; //   02:00 local, first single instant

  test('local times inside the fold yield two UTC candidates one hour apart', () => {
    const mid = foldStartLocal + 30 * MIN;
    const result = localToUtc(eastern, mid);
    expect(result.kind).toBe('ambiguous');
    if (result.kind === 'ambiguous') {
      expect(result.laterUtcMs - result.earlierUtcMs).toBe(HOUR);
      expect(result.earlierUtcMs).toBe(FALL_UTC - 30 * MIN); // still UTC-4
      expect(result.laterUtcMs).toBe(FALL_UTC + 30 * MIN); // now UTC-5
    }
  });

  test('fold boundaries are unique', () => {
    expect(localToUtc(eastern, foldStartLocal - 1).kind).toBe('unique');
    expect(localToUtc(eastern, foldEndLocal).kind).toBe('unique');
  });

  test('resolveLocal requires an explicit choice in the fold', () => {
    const mid = foldStartLocal + 15 * MIN;
    expect(() => resolveLocal(eastern, mid)).toThrow(AmbiguousLocalTimeError);
    expect(resolveLocal(eastern, mid, 'earlier')).toBe(FALL_UTC - 45 * MIN);
    expect(resolveLocal(eastern, mid, 'later')).toBe(FALL_UTC + 15 * MIN);
  });
});

describe('non-integer-hour offsets', () => {
  test('UTC+5:45 maps local to UTC with a 345-minute offset', () => {
    const local = civilMs(2024, 1, 10, 12, 0, 0); // before the Jan 15 transition
    const result = localToUtc(nepal, local);
    expect(result).toEqual({ kind: 'unique', utcMs: local - 345 * MIN });
    expect(utcToLocal(nepal, local - 345 * MIN).localMs).toBe(local);
  });

  test('15-minute fold between +5:45 and +5:30', () => {
    const foldStartLocal = NP_FOLD_UTC + 330 * MIN;
    const mid = foldStartLocal + 7 * MIN;
    const result = localToUtc(nepal, mid);
    expect(result.kind).toBe('ambiguous');
    if (result.kind === 'ambiguous') {
      expect(result.laterUtcMs - result.earlierUtcMs).toBe(15 * MIN);
    }
    // Just outside the 15-minute fold: unique again.
    expect(localToUtc(nepal, foldStartLocal + 15 * MIN).kind).toBe('unique');
  });

  test('15-minute gap between +5:30 and +5:45', () => {
    const gapStartLocal = NP_GAP_UTC + 330 * MIN;
    expect(localToUtc(nepal, gapStartLocal + 7 * MIN)).toEqual({ kind: 'none' });
    expect(localToUtc(nepal, gapStartLocal + 15 * MIN).kind).toBe('unique');
  });

  test('30-minute Lord Howe style gap and fold', () => {
    const gapLocal = LH_FWD_UTC + 630 * MIN + 15 * MIN; // halfway into the 30-min gap
    expect(localToUtc(lordHowe, gapLocal)).toEqual({ kind: 'none' });

    const foldLocal = LH_BACK_UTC + 630 * MIN + 15 * MIN; // halfway into the 30-min fold
    const result = localToUtc(lordHowe, foldLocal);
    expect(result.kind).toBe('ambiguous');
    if (result.kind === 'ambiguous') {
      expect(result.laterUtcMs - result.earlierUtcMs).toBe(30 * MIN);
    }
  });
});

describe('interval resolution', () => {
  test('plain interval resolves with positive length', () => {
    const iv = resolveInterval(eastern, civilMs(2024, 3, 9, 12, 0), civilMs(2024, 3, 9, 14, 0));
    expect(iv.durationMs).toBe(2 * HOUR);
  });

  test('interval across spring forward is one hour shorter than wall clock', () => {
    const iv = resolveInterval(eastern, civilMs(2024, 3, 10, 1, 0), civilMs(2024, 3, 10, 4, 0));
    expect(iv.durationMs).toBe(2 * HOUR);
  });

  test('interval across fall back is one hour longer than wall clock', () => {
    const iv = resolveInterval(eastern, civilMs(2024, 11, 3, 0, 30), civilMs(2024, 11, 3, 2, 30));
    expect(iv.durationMs).toBe(3 * HOUR);
  });

  test('fold endpoints are chosen independently', () => {
    const start = civilMs(2024, 11, 3, 1, 30);
    const end = civilMs(2024, 11, 3, 1, 45);
    expect(() => resolveInterval(eastern, start, end)).toThrow(AmbiguousLocalTimeError);
    const iv = resolveInterval(eastern, start, end, { start: 'earlier', end: 'later' });
    expect(iv.durationMs).toBe(75 * MIN); // 15 wall minutes + 1 repeated hour
  });

  test('endpoint in a gap throws', () => {
    expect(() => resolveInterval(eastern, civilMs(2024, 3, 10, 2, 30), civilMs(2024, 3, 10, 4, 0))).toThrow(
      NonexistentLocalTimeError,
    );
  });

  test('inverted or zero-length UTC results are rejected', () => {
    // 01:45 (later) -> 01:15 (earlier) on fall-back day goes backwards in UTC.
    expect(() =>
      resolveInterval(eastern, civilMs(2024, 11, 3, 1, 45), civilMs(2024, 11, 3, 1, 15), {
        start: 'later',
        end: 'earlier',
      }),
    ).toThrow(NonPositiveIntervalError);
    // Same instant chosen for both ends has zero length.
    expect(() =>
      resolveInterval(eastern, civilMs(2024, 11, 3, 1, 30), civilMs(2024, 11, 3, 1, 30), {
        start: 'earlier',
        end: 'earlier',
      }),
    ).toThrow(NonPositiveIntervalError);
  });
});

describe('UTC-mapping enumeration cross-check', () => {
  interface Expectation {
    table: TransitionTable;
    transitionUtc: number;
    offsetBefore: number;
    offsetAfter: number;
  }

  const cases: Expectation[] = [
    { table: eastern, transitionUtc: SPRING_UTC, offsetBefore: -300, offsetAfter: -240 },
    { table: eastern, transitionUtc: FALL_UTC, offsetBefore: -240, offsetAfter: -300 },
    { table: nepal, transitionUtc: NP_FOLD_UTC, offsetBefore: 345, offsetAfter: 330 },
    { table: nepal, transitionUtc: NP_GAP_UTC, offsetBefore: 330, offsetAfter: 345 },
    { table: lordHowe, transitionUtc: LH_FWD_UTC, offsetBefore: 630, offsetAfter: 660 },
    { table: lordHowe, transitionUtc: LH_BACK_UTC, offsetBefore: 660, offsetAfter: 630 },
  ];

  for (const { table, transitionUtc, offsetBefore, offsetAfter } of cases) {
    const deltaMin = offsetAfter - offsetBefore;
    const label = deltaMin > 0 ? `gap +${deltaMin}min` : `fold ${deltaMin}min`;

    test(`enumeration around transition at ${new Date(transitionUtc).toISOString()} (${label})`, () => {
      const span = 3 * HOUR;
      const mappings = enumerateUtcMapping(table, transitionUtc - span, transitionUtc + span, MIN);

      // Every enumerated UTC instant round-trips through localToUtc.
      const hitCount = new Map<number, number>();
      for (const { utcMs, localMs, offsetMinutes } of mappings) {
        expect(utcMs + offsetMinutes * MIN).toBe(localMs);
        const back = localToUtc(table, localMs);
        if (back.kind === 'unique') expect(back.utcMs).toBe(utcMs);
        else if (back.kind === 'ambiguous') expect([back.earlierUtcMs, back.laterUtcMs]).toContain(utcMs);
        else throw new Error(`local ${localMs} unexpectedly nonexistent`);
        hitCount.set(localMs, (hitCount.get(localMs) ?? 0) + 1);
      }

      const foldStartLocal = transitionUtc + Math.min(offsetBefore, offsetAfter) * MIN;
      const foldEndLocal = transitionUtc + Math.max(offsetBefore, offsetAfter) * MIN;
      if (deltaMin < 0) {
        // Fold: every local minute in the repeated range is hit exactly twice.
        for (let l = foldStartLocal; l < foldEndLocal; l += MIN) {
          expect(hitCount.get(l)).toBe(2);
        }
      } else {
        // Gap: no local minute in the skipped range is ever hit, and each is
        // reported nonexistent by localToUtc.
        for (let l = foldStartLocal; l < foldEndLocal; l += MIN) {
          expect(hitCount.get(l) ?? 0).toBe(0);
          expect(localToUtc(table, l).kind).toBe('none');
        }
      }
      // Outside the transition neighborhood every local minute is hit once.
      expect(hitCount.get(transitionUtc + offsetBefore * MIN - 2 * HOUR)).toBe(1);
      expect(hitCount.get(transitionUtc + offsetAfter * MIN + 2 * HOUR)).toBe(1);
    });
  }
});

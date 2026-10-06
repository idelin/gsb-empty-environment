import test from 'node:test';
import assert from 'node:assert/strict';
import {
  accumulate,
  buildEffectiveCalendar,
  subtractPauseDays,
  unionIntervals,
  MS_PER_MINUTE as MIN,
  MS_PER_DAY as DAY,
  type Interval,
} from '../src/calendar.ts';

const T = (iso: string): number => Date.parse(`${iso}Z`);
const iso = (ms: number): string => new Date(ms).toISOString();

// ---------------------------------------------------------------------------
// Minute-by-minute finite calendar oracle.
// A minute slot [m, m+1min) is open iff it lies fully inside the effective
// calendar. All fixtures are minute-aligned so this is an exact model.
// ---------------------------------------------------------------------------

interface OracleResult {
  end: number;
  complete: boolean;
  accumulatedMs: number;
  consumed: number[]; // indices of consumed minute slots
}

function minuteGrid(open: Interval[], pauseDays: string[], w0: number, w1: number): boolean[] {
  const eff = buildEffectiveCalendar(open, pauseDays);
  const grid = new Array<boolean>((w1 - w0) / MIN).fill(false);
  for (const iv of eff) {
    const s = Math.max(iv.start, w0);
    const e = Math.min(iv.end, w1);
    for (let m = Math.ceil((s - w0) / MIN); m < Math.floor((e - w0) / MIN); m++) {
      grid[m] = true;
    }
  }
  return grid;
}

function oracle(
  open: Interval[],
  pauseDays: string[],
  w0: number,
  w1: number,
  from: number,
  durationMs: number,
): OracleResult {
  const grid = minuteGrid(open, pauseDays, w0, w1);
  let need = Math.abs(durationMs) / MIN;
  const consumed: number[] = [];
  if (durationMs >= 0) {
    let i = (from - w0) / MIN;
    while (need > 0 && i < grid.length) {
      if (grid[i]) {
        consumed.push(i);
        need--;
      }
      i++;
    }
    return {
      end: consumed.length === 0 ? from : w0 + (consumed[consumed.length - 1] + 1) * MIN,
      complete: need === 0,
      accumulatedMs: consumed.length * MIN,
      consumed,
    };
  }
  let i = (from - w0) / MIN - 1;
  while (need > 0 && i >= 0) {
    if (grid[i]) {
      consumed.push(i);
      need--;
    }
    i--;
  }
  consumed.reverse();
  return {
    end: consumed.length === 0 ? from : w0 + consumed[0] * MIN,
    complete: need === 0,
    accumulatedMs: consumed.length * MIN,
    consumed,
  };
}

function assertEvidenceMatchesOracle(
  result: ReturnType<typeof accumulate>,
  expected: OracleResult,
  w0: number,
): void {
  assert.equal(result.complete, expected.complete, 'complete flag');
  assert.equal(result.accumulatedMs, expected.accumulatedMs, 'accumulated ms');
  assert.equal(result.end, expected.end, `end (${iso(result.end)} vs ${iso(expected.end)})`);
  // Evidence must reconstruct exactly the consumed minute slots.
  const covered = new Set<number>();
  for (const ev of result.evidence) {
    for (let t = ev.start; t < ev.end; t += MIN) covered.add((t - w0) / MIN);
  }
  assert.deepEqual([...covered].sort((a, b) => a - b), expected.consumed, 'evidence minutes');
}

function assertEvidenceWellFormed(
  result: ReturnType<typeof accumulate>,
  effective: Interval[],
): void {
  let total = 0;
  for (let i = 0; i < result.evidence.length; i++) {
    const ev = result.evidence[i];
    assert.ok(ev.end > ev.start, 'evidence interval non-empty');
    if (i > 0) {
      assert.ok(ev.start >= result.evidence[i - 1].end, 'evidence chronological, disjoint');
    }
    assert.ok(
      effective.some((iv) => iv.start <= ev.start && ev.end <= iv.end),
      `evidence ${iso(ev.start)}..${iso(ev.end)} inside effective open time`,
    );
    total += ev.end - ev.start;
  }
  assert.equal(total, result.accumulatedMs, 'evidence totals accumulated ms');
}

// ---------------------------------------------------------------------------
// Unit tests: union and pause-day subtraction
// ---------------------------------------------------------------------------

test('unionIntervals merges overlapping and touching intervals', () => {
  const merged = unionIntervals([
    { start: T('2026-01-05T10:00:00'), end: T('2026-01-05T12:00:00') },
    { start: T('2026-01-05T09:00:00'), end: T('2026-01-05T10:00:00') }, // touching
    { start: T('2026-01-05T11:00:00'), end: T('2026-01-05T13:00:00') }, // overlapping
    { start: T('2026-01-05T15:00:00'), end: T('2026-01-05T16:00:00') }, // disjoint
    { start: T('2026-01-05T18:00:00'), end: T('2026-01-05T18:00:00') }, // empty, dropped
  ]);
  assert.deepEqual(merged, [
    { start: T('2026-01-05T09:00:00'), end: T('2026-01-05T13:00:00') },
    { start: T('2026-01-05T15:00:00'), end: T('2026-01-05T16:00:00') },
  ]);
});

test('subtractPauseDays splits intervals and removes covered days', () => {
  const open = [
    { start: T('2026-01-05T08:00:00'), end: T('2026-01-05T20:00:00') }, // fully paused
    { start: T('2026-01-06T08:00:00'), end: T('2026-01-07T20:00:00') }, // split by pause
  ];
  const eff = subtractPauseDays(unionIntervals(open), ['2026-01-05', '2026-01-07']);
  assert.deepEqual(eff, [
    { start: T('2026-01-06T08:00:00'), end: T('2026-01-07T00:00:00') },
  ]);
});

// ---------------------------------------------------------------------------
// Directed scenarios, each cross-checked against the minute oracle
// ---------------------------------------------------------------------------

const WORKWEEK: Interval[] = [5, 6, 7, 8, 9].map((d) => ({
  start: T(`2026-01-0${d}T09:00:00`),
  end: T(`2026-01-0${d}T17:00:00`),
}));
const W0 = T('2026-01-04T00:00:00');
const W1 = T('2026-01-12T00:00:00');

function check(
  open: Interval[],
  pauseDays: string[],
  from: number,
  durationMs: number,
): ReturnType<typeof accumulate> {
  const result = accumulate(open, from, durationMs, pauseDays);
  const expected = oracle(open, pauseDays, W0, W1, from, durationMs);
  assertEvidenceMatchesOracle(result, expected, W0);
  assertEvidenceWellFormed(result, buildEffectiveCalendar(open, pauseDays));
  return result;
}

test('cross-day forward accumulation with evidence', () => {
  const r = check(WORKWEEK, [], T('2026-01-05T10:00:00'), 14 * 60 * MIN);
  assert.equal(iso(r.end), '2026-01-06T16:00:00.000Z');
  assert.equal(r.complete, true);
  assert.deepEqual(
    r.evidence.map((ev) => [iso(ev.start), iso(ev.end)]),
    [
      ['2026-01-05T10:00:00.000Z', '2026-01-05T17:00:00.000Z'],
      ['2026-01-06T09:00:00.000Z', '2026-01-06T16:00:00.000Z'],
    ],
  );
});

test('finishing exactly at the right endpoint is legal', () => {
  const r = check(WORKWEEK, [], T('2026-01-05T10:00:00'), 7 * 60 * MIN);
  assert.equal(r.complete, true);
  assert.equal(iso(r.end), '2026-01-05T17:00:00.000Z');
});

test('overlapping open intervals are unioned, not double counted', () => {
  const open = [
    { start: T('2026-01-05T09:00:00'), end: T('2026-01-05T13:00:00') },
    { start: T('2026-01-05T12:00:00'), end: T('2026-01-05T17:00:00') },
  ];
  const r = check(open, [], T('2026-01-05T09:00:00'), 8 * 60 * MIN);
  assert.equal(r.complete, true);
  assert.equal(iso(r.end), '2026-01-05T17:00:00.000Z');
  assert.equal(r.accumulatedMs, 8 * 60 * MIN);
});

test('full pause day contributes zero and is skipped', () => {
  const r = check(WORKWEEK, ['2026-01-06'], T('2026-01-05T10:00:00'), 14 * 60 * MIN);
  assert.equal(iso(r.end), '2026-01-07T16:00:00.000Z');
  assert.ok(
    r.evidence.every((ev) => iso(ev.start).slice(0, 10) !== '2026-01-06'),
    'no evidence on the paused day',
  );
});

test('pause day covers all open time of that day', () => {
  const open = [{ start: T('2026-01-05T00:00:00'), end: T('2026-01-06T00:00:00') }];
  const r = check(open, ['2026-01-05'], T('2026-01-05T00:00:00'), 60 * MIN);
  assert.equal(r.complete, false);
  assert.equal(r.accumulatedMs, 0);
  assert.equal(r.evidence.length, 0);
});

test('insufficient open time reports remaining', () => {
  const r = check(WORKWEEK, [], T('2026-01-09T10:00:00'), 10 * 60 * MIN);
  assert.equal(r.complete, false);
  assert.equal(r.accumulatedMs, 7 * 60 * MIN);
  assert.equal(r.remainingMs, 3 * 60 * MIN);
  assert.equal(iso(r.end), '2026-01-09T17:00:00.000Z');
});

test('negative duration deducts backward across days', () => {
  const r = check(WORKWEEK, [], T('2026-01-07T12:00:00'), -10 * 60 * MIN);
  assert.equal(r.complete, true);
  assert.equal(iso(r.end), '2026-01-06T10:00:00.000Z');
  assert.deepEqual(
    r.evidence.map((ev) => [iso(ev.start), iso(ev.end)]),
    [
      ['2026-01-06T10:00:00.000Z', '2026-01-06T17:00:00.000Z'],
      ['2026-01-07T09:00:00.000Z', '2026-01-07T12:00:00.000Z'],
    ],
  );
});

test('backward deduction landing exactly on a left endpoint is legal', () => {
  const r = check(WORKWEEK, [], T('2026-01-06T17:00:00'), -8 * 60 * MIN);
  assert.equal(r.complete, true);
  assert.equal(iso(r.end), '2026-01-06T09:00:00.000Z');
});

test('backward deduction skips full pause days and weekends', () => {
  const r = check(WORKWEEK, ['2026-01-06'], T('2026-01-07T12:00:00'), -10 * 60 * MIN);
  assert.equal(r.complete, true);
  assert.equal(iso(r.end), '2026-01-05T10:00:00.000Z');
  assert.ok(
    r.evidence.every((ev) => iso(ev.start).slice(0, 10) !== '2026-01-06'),
    'no evidence on the paused day',
  );
});

test('zero duration completes immediately with empty evidence', () => {
  const r = check(WORKWEEK, [], T('2026-01-05T10:00:00'), 0);
  assert.equal(r.complete, true);
  assert.equal(r.end, r.from);
  assert.equal(r.evidence.length, 0);
});

// ---------------------------------------------------------------------------
// Randomized cross-check against the minute-by-minute oracle
// ---------------------------------------------------------------------------

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test('randomized calendars match the minute-by-minute oracle', () => {
  const rand = mulberry32(0x5eed);
  const pick = (n: number) => Math.floor(rand() * n);
  const dayIso = (d: number) => `2026-03-0${d + 1}`;

  for (let iter = 0; iter < 400; iter++) {
    const w0 = T('2026-03-01T00:00:00');
    const w1 = w0 + 8 * DAY;
    const spanMin = (w1 - w0) / MIN;

    const open: Interval[] = [];
    const nIv = 1 + pick(6);
    for (let k = 0; k < nIv; k++) {
      const s = pick(spanMin - 1);
      const len = 1 + pick(Math.min(spanMin - s, 2 * 24 * 60));
      open.push({ start: w0 + s * MIN, end: w0 + (s + len) * MIN });
    }

    const pauseDays: string[] = [];
    for (let d = 0; d < 7; d++) {
      if (rand() < 0.25) pauseDays.push(dayIso(d));
    }

    const from = w0 + pick(spanMin) * MIN;
    const durationMs = (pick(2) === 0 ? 1 : -1) * pick(3 * 24 * 60) * MIN;

    const result = accumulate(open, from, durationMs, pauseDays);
    const expected = oracle(open, pauseDays, w0, w1, from, durationMs);
    try {
      assertEvidenceMatchesOracle(result, expected, w0);
      assertEvidenceWellFormed(result, buildEffectiveCalendar(open, pauseDays));
    } catch (err) {
      throw new Error(
        `iter=${iter} from=${iso(from)} durationMin=${durationMs / MIN} ` +
          `pauseDays=${pauseDays.join(',')} open=${JSON.stringify(
            open.map((iv) => [iso(iv.start), iso(iv.end)]),
          )}\n${(err as Error).message}`,
      );
    }
  }
});

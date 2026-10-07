import { test } from "node:test";
import assert from "node:assert/strict";
import {
  expand,
  expandCandidates,
  expandPage,
  expandRange,
  instancesOn,
  instanceId,
  lastCandidateSerial,
  weekdayOf,
  weekStartOf,
  type Instance,
  type MoveOverride,
  type WeeklyRule,
} from "../src/weekly.ts";

// Serial 0 is a Monday. Rule: every 2 weeks on Mon/Wed/Fri, starting
// serial 2 (Wednesday of week 0), 10 pre-exclusion candidates.
// Candidates: 2, 4, 14, 16, 18, 28, 30, 32, 42, 44.
const baseRule: WeeklyRule = {
  id: "standup",
  startSerial: 2,
  intervalWeeks: 2,
  weekdays: [4, 0, 2], // intentionally unsorted, with duplicates possible
  count: 10,
};

const EXPECTED_CANDIDATES = [2, 4, 14, 16, 18, 28, 30, 32, 42, 44];

/** Day-by-day scan: collect every instance by walking serial by serial. */
function scanDayByDay(
  rule: WeeklyRule,
  fromSerial: number,
  toSerial: number,
  moves: MoveOverride[] = [],
): Instance[] {
  const found: Instance[] = [];
  for (let serial = fromSerial; serial <= toSerial; serial++) {
    found.push(...instancesOn(rule, serial, moves));
  }
  return found;
}

test("weekday/weekStart helpers anchor serial 0 to Monday", () => {
  assert.equal(weekdayOf(0), 0);
  assert.equal(weekdayOf(6), 6);
  assert.equal(weekdayOf(7), 0);
  assert.equal(weekdayOf(-1), 6);
  assert.equal(weekStartOf(0), 0);
  assert.equal(weekStartOf(6), 0);
  assert.equal(weekStartOf(7), 7);
  assert.equal(weekStartOf(-1), -7);
});

test("candidates honor interval, weekday set, and startSerial", () => {
  assert.deepEqual(expandCandidates(baseRule), EXPECTED_CANDIDATES);
  // Every candidate falls on Mon/Wed/Fri of an on-interval week.
  for (const serial of EXPECTED_CANDIDATES) {
    assert.ok([0, 2, 4].includes(weekdayOf(serial)), `serial ${serial}`);
    assert.equal((weekStartOf(serial) / 7) % 2, 0, `serial ${serial}`);
  }
});

test("count is applied before exclusions: exdates shrink, never extend", () => {
  const rule: WeeklyRule = { ...baseRule, exdates: [16, 44] };
  const instances = expand(rule);
  assert.equal(instances.length, 8);
  assert.deepEqual(
    instances.map((i) => i.serial),
    [2, 4, 14, 18, 28, 30, 32, 42],
  );
  // The excluded last candidate (44) is not replaced by a later occurrence.
  assert.equal(lastCandidateSerial(rule), 44);
  assert.equal(Math.max(...instances.map((i) => i.serial)), 42);
});

test("paginated expansion concatenates to the full expansion", () => {
  const rule: WeeklyRule = { ...baseRule, exdates: [16] };
  const full = expand(rule);
  assert.equal(full.length, 9);

  const collected: Instance[] = [];
  const pages = [];
  for (let offset = 0; ; offset += 3) {
    const page = expandPage(rule, { offset, limit: 3 });
    pages.push(page);
    collected.push(...page.instances);
    assert.equal(page.total, 9);
    if (!page.hasMore) break;
  }
  assert.deepEqual(collected, full);
  assert.deepEqual(
    pages.map((p) => p.instances.length),
    [3, 3, 3],
  );
  assert.deepEqual(
    pages.map((p) => p.hasMore),
    [true, true, false],
  );
  // Offset beyond the end yields an empty final page.
  const beyond = expandPage(rule, { offset: 9, limit: 3 });
  assert.deepEqual(beyond.instances, []);
  assert.equal(beyond.hasMore, false);
});

test("day-by-day scan matches expansion and paginated expansion", () => {
  const rule: WeeklyRule = { ...baseRule, exdates: [16] };
  const scanned = scanDayByDay(rule, 0, 44);
  const full = expand(rule);
  assert.deepEqual(scanned, full);

  const paged: Instance[] = [];
  for (let offset = 0; ; offset += 4) {
    const page = expandPage(rule, { offset, limit: 4 });
    paged.push(...page.instances);
    if (!page.hasMore) break;
  }
  assert.deepEqual(paged, scanned);
});

test("rescheduling onto another instance's date keeps the original id", () => {
  // Move the occurrence of serial 14 onto serial 18, where another
  // instance already lives.
  const moves: MoveOverride[] = [{ originalSerial: 14, newSerial: 18 }];
  const instances = expand(baseRule, moves);

  // Total count is unchanged; the moved instance keeps its id.
  assert.equal(instances.length, 10);
  const moved = instances.find((i) => i.id === instanceId("standup", 14));
  assert.ok(moved);
  assert.equal(moved.serial, 18);
  assert.equal(moved.originalSerial, 14);
  assert.equal(moved.moved, true);

  // Day-by-day scan: serial 14 is now empty, serial 18 holds both ids.
  assert.deepEqual(instancesOn(baseRule, 14, moves), []);
  assert.deepEqual(
    instancesOn(baseRule, 18, moves).map((i) => i.id),
    [instanceId("standup", 14), instanceId("standup", 18)],
  );
  // The natural instance on 18 is untouched.
  const natural = instancesOn(baseRule, 18, moves).find(
    (i) => i.id === instanceId("standup", 18),
  );
  assert.ok(natural);
  assert.equal(natural.moved, false);

  // Full scan still agrees with the expansion under moves.
  assert.deepEqual(scanDayByDay(baseRule, 0, 44, moves), instances);
});

test("termination boundary: nothing after the count-th candidate", () => {
  const last = lastCandidateSerial(baseRule);
  assert.equal(last, 44);

  // No instance exists on any serial past the boundary, scanned day by day.
  assert.deepEqual(scanDayByDay(baseRule, 45, 45 + 100), []);
  assert.deepEqual(expandRange(baseRule, 45, 10_000), []);

  // The boundary day itself does hold the final instance.
  assert.deepEqual(
    instancesOn(baseRule, 44).map((i) => i.id),
    [instanceId("standup", 44)],
  );

  // count = 1 terminates immediately after the first candidate.
  const single: WeeklyRule = { ...baseRule, count: 1 };
  assert.deepEqual(
    expand(single).map((i) => i.serial),
    [2],
  );
  assert.deepEqual(scanDayByDay(single, 3, 100), []);

  // count = 0 yields an empty series.
  assert.deepEqual(expand({ ...baseRule, count: 0 }), []);
});

test("invalid rules are rejected", () => {
  assert.throws(() => expand({ ...baseRule, intervalWeeks: 0 }), RangeError);
  assert.throws(() => expand({ ...baseRule, weekdays: [] }), RangeError);
  assert.throws(() => expand({ ...baseRule, weekdays: [7] }), RangeError);
  assert.throws(() => expand({ ...baseRule, count: -1 }), RangeError);
  assert.throws(() => expandPage(baseRule, { limit: 0 }), RangeError);
});

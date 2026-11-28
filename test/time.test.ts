import { test } from "node:test";
import assert from "node:assert/strict";

import {
  addWallDuration,
  localWeekday,
  resolveWall,
  utcOffset,
  wallPartsAt,
  toIso,
} from "../src/time.js";
import { HOUR, MINUTE, wall } from "./helpers.js";

const NY = "America/New_York";
const BERLIN = "Europe/Berlin";

test("New York spring-forward: daily 09:00 handoff spans only 23 hours", () => {
  const friday = wall(NY, 2026, 3, 6, 9);
  const saturday = addWallDuration(NY, friday, { days: 1 });
  const sunday = addWallDuration(NY, saturday, { days: 1 });
  const monday = addWallDuration(NY, sunday, { days: 1 });

  assert.equal(saturday - friday, 24 * HOUR);
  assert.equal(sunday - saturday, 23 * HOUR);
  assert.equal(monday - sunday, 24 * HOUR);

  for (const instant of [friday, saturday, sunday, monday]) {
    assert.equal(wallPartsAt(NY, instant).hour, 9);
    assert.equal(wallPartsAt(NY, instant).minute, 0);
  }

  assert.equal(utcOffset(NY, friday) / HOUR, -5);
  assert.equal(utcOffset(NY, monday) / HOUR, -4);
});

test("New York fall-back: daily 09:00 handoff spans 25 hours", () => {
  const friday = wall(NY, 2026, 10, 30, 9);
  const saturday = addWallDuration(NY, friday, { days: 1 });
  const sunday = addWallDuration(NY, saturday, { days: 1 });
  const monday = addWallDuration(NY, sunday, { days: 1 });

  assert.equal(saturday - friday, 24 * HOUR);
  assert.equal(sunday - saturday, 25 * HOUR);
  assert.equal(monday - sunday, 24 * HOUR);

  for (const instant of [friday, saturday, sunday, monday]) {
    assert.equal(wallPartsAt(NY, instant).hour, 9);
  }
});

test("Europe/Berlin spring and fall transitions are 23h and 25h at 09:00", () => {
  const beforeSpring = wall(BERLIN, 2026, 3, 28, 9);
  const springDay = addWallDuration(BERLIN, beforeSpring, { days: 1 });
  const afterSpring = addWallDuration(BERLIN, springDay, { days: 1 });
  assert.equal(springDay - beforeSpring, 23 * HOUR);
  assert.equal(afterSpring - springDay, 24 * HOUR);

  const beforeFall = wall(BERLIN, 2026, 10, 24, 9);
  const fallDay = addWallDuration(BERLIN, beforeFall, { days: 1 });
  const afterFall = addWallDuration(BERLIN, fallDay, { days: 1 });
  assert.equal(fallDay - beforeFall, 25 * HOUR);
  assert.equal(afterFall - fallDay, 24 * HOUR);
});

test("spring-forward gap wall time is clamped to the first post-gap instant", () => {
  const skipped = resolveWall(NY, {
    year: 2026,
    month: 3,
    day: 8,
    hour: 2,
    minute: 30,
    second: 0,
    millisecond: 0,
  });
  assert.equal(skipped.skipped, true);
  assert.equal(skipped.ambiguous, false);
  assert.equal(toIso(skipped.instant), "2026-03-08T07:00:00.000Z");
  assert.equal(wallPartsAt(NY, skipped.instant).hour, 3);
});

test("fall-back overlap resolves to the earlier or later occurrence explicitly", () => {
  const base = {
    year: 2026,
    month: 11,
    day: 1,
    hour: 1,
    minute: 30,
    second: 0,
    millisecond: 0,
  };
  const earlier = resolveWall(NY, base, "earlier");
  const later = resolveWall(NY, base, "later");
  const compatible = resolveWall(NY, base);

  assert.equal(earlier.ambiguous, true);
  assert.equal(later.ambiguous, true);
  assert.equal(later.instant - earlier.instant, HOUR);
  assert.equal(toIso(earlier.instant), "2026-11-01T05:30:00.000Z");
  assert.equal(toIso(later.instant), "2026-11-01T06:30:00.000Z");
  assert.equal(compatible.instant, earlier.instant);
});

test("wall arithmetic crossing midnight keeps the requested local wall time", () => {
  const start = wall(NY, 2026, 3, 5, 22);
  const end = addWallDuration(NY, start, { hours: 8 });
  const endWall = wallPartsAt(NY, end);
  assert.equal(endWall.day, 6);
  assert.equal(endWall.hour, 6);
  assert.equal(end - start, 8 * HOUR);
});

test("local weekday is computed from zone wall time, not UTC", () => {
  const instant = wall(NY, 2026, 3, 8, 23);
  assert.equal(localWeekday(NY, instant), 0);
  assert.equal(new Date(instant).getUTCDay(), 1);
});

test("minute-precision offsets and sub-hour durations stay exact", () => {
  const instant = wall(NY, 2026, 6, 15, 12, 30);
  const plus = addWallDuration(NY, instant, { minutes: 45 });
  assert.equal(plus - instant, 45 * MINUTE);
  assert.equal(wallPartsAt(NY, plus).hour, 13);
  assert.equal(wallPartsAt(NY, plus).minute, 15);
});

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Calendar } from '../src/calendar.ts';

// UTC+8, Monday-Friday, 09:00-17:00 local (480 working minutes per day).
const calendar = new Calendar({
  timeZoneOffsetMinutes: 480,
  workDays: [1, 2, 3, 4, 5],
  dayStartMinutes: 9 * 60,
  dayEndMinutes: 17 * 60,
});

// Monday 2026-01-05 09:00 +08:00.
const projectStart = new Date('2026-01-05T01:00:00Z');

test('offset 0 is the project start itself', () => {
  assert.equal(calendar.toWallClock(0, projectStart).getTime(), projectStart.getTime());
});

test('one full workday lands on the same-day close of business', () => {
  assert.equal(
    calendar.format(calendar.toWallClock(480, projectStart)),
    '2026-01-05T17:00:00+08:00',
  );
});

test('weekends are skipped when projecting offsets', () => {
  // 2399 = 4 workdays + 479 min  =>  Friday 16:59.
  assert.equal(
    calendar.format(calendar.toWallClock(2399, projectStart)),
    '2026-01-09T16:59:00+08:00',
  );
  // 2401 = 4 workdays + 481 min  =>  over the weekend to Monday 09:01.
  assert.equal(
    calendar.format(calendar.toWallClock(2401, projectStart)),
    '2026-01-12T09:01:00+08:00',
  );
});

test('addWorkTime spans a weekend', () => {
  const friday16 = new Date('2026-01-09T08:00:00Z'); // 16:00 +08 Friday
  assert.equal(calendar.format(calendar.addWorkTime(friday16, 120)), '2026-01-12T10:00:00+08:00');
});

test('isWorkingMoment respects weekdays and hours in local time', () => {
  assert.equal(calendar.isWorkingMoment(new Date('2026-01-05T01:00:00Z')), true); // Mon 09:00
  assert.equal(calendar.isWorkingMoment(new Date('2026-01-05T09:00:00Z')), false); // Mon 17:00
  assert.equal(calendar.isWorkingMoment(new Date('2026-01-10T04:00:00Z')), false); // Sat 12:00
});

test('workMinutesBetween counts only working minutes', () => {
  const end = calendar.toWallClock(2399, projectStart);
  assert.equal(calendar.workMinutesBetween(projectStart, end), 2399);
});

test('format renders the configured timezone offset', () => {
  const utcPlus1 = new Calendar({
    timeZoneOffsetMinutes: 60,
    workDays: [1, 2, 3, 4, 5],
    dayStartMinutes: 9 * 60,
    dayEndMinutes: 17 * 60,
  });
  assert.equal(utcPlus1.format(new Date('2026-01-05T08:00:00Z')), '2026-01-05T09:00:00+01:00');
  assert.equal(calendar.format(projectStart), '2026-01-05T09:00:00+08:00');
});

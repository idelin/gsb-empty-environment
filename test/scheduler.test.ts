import { test } from 'node:test';
import assert from 'node:assert/strict';
import { schedule } from '../src/scheduler.ts';
import { absInterval } from '../src/model.ts';
import type { ScheduleInput, ScheduleSuccess } from '../src/types.ts';

const asSuccess = (r: ReturnType<typeof schedule>): ScheduleSuccess => {
  assert.ok(r.status === 'optimal' || r.status === 'feasible', `期望可行，得到 ${r.status}`);
  return r;
};

test('跨夜班次按真实起止分钟展开', () => {
  assert.deepEqual(absInterval(0, 22 * 60, 6 * 60), [22 * 60, 24 * 60 + 6 * 60]);
  assert.deepEqual(absInterval(6, 22 * 60, 6 * 60), [6 * 1440 + 1320, 7 * 1440 + 360]);
  assert.deepEqual(absInterval(1, 9 * 60, 17 * 60), [1440 + 540, 1440 + 1020]);
  // 午夜 00:00 结束的班次
  assert.deepEqual(absInterval(0, 16 * 60, 0), [960, 1440]);
});

test('带权偏好：prefer 命中时损失为 0，avoid 被绕开', () => {
  const base = {
    employees: [
      { id: 'E1', skills: ['a'], maxDailyMinutes: 600 },
      { id: 'E2', skills: ['a'], maxDailyMinutes: 600 },
    ],
    shifts: [{ id: 's1', day: 0, startMinute: 9 * 60, endMinute: 17 * 60, skill: 'a' }],
    minRestMinutes: 0,
  };
  const prefer = asSuccess(
    schedule({ ...base, preferences: [{ employee: 'E1', type: 'prefer', weight: 5, shiftId: 's1' }] }),
  );
  assert.equal(prefer.status, 'optimal');
  assert.deepEqual(prefer.assignments[0]!.employeeIds, ['E1']);
  assert.equal(prefer.totalLoss, 0);

  const avoid = asSuccess(
    schedule({ ...base, preferences: [{ employee: 'E1', type: 'avoid', weight: 5, shiftId: 's1' }] }),
  );
  assert.deepEqual(avoid.assignments[0]!.employeeIds, ['E2']);
  assert.equal(avoid.totalLoss, 0);
});

test('偏好无法满足时按权重计入损失', () => {
  const r = asSuccess(
    schedule({
      employees: [{ id: 'E1', skills: ['a'], maxDailyMinutes: 600 }],
      shifts: [{ id: 's1', day: 0, startMinute: 9 * 60, endMinute: 17 * 60, skill: 'a' }],
      minRestMinutes: 0,
      preferences: [{ employee: 'E1', type: 'avoid', weight: 7, shiftId: 's1' }],
    }),
  );
  assert.equal(r.totalLoss, 7);
  assert.equal(r.losses.length, 1);
});

test('技能约束：无对应技能的员工不会被排入', () => {
  const r = asSuccess(
    schedule({
      employees: [
        { id: 'E1', skills: ['a'], maxDailyMinutes: 600 },
        { id: 'E2', skills: ['b'], maxDailyMinutes: 600 },
      ],
      shifts: [{ id: 's1', day: 0, startMinute: 9 * 60, endMinute: 17 * 60, skill: 'b' }],
      minRestMinutes: 0,
    }),
  );
  assert.deepEqual(r.assignments[0]!.employeeIds, ['E2']);
});

test('每日工时为硬约束：同一天两个 8 小时班不能排给上限 480 分钟的同一人', () => {
  const input: ScheduleInput = {
    employees: [{ id: 'E1', skills: ['a'], maxDailyMinutes: 480 }],
    shifts: [
      { id: 's1', day: 0, startMinute: 8 * 60, endMinute: 16 * 60, skill: 'a' },
      { id: 's2', day: 0, startMinute: 16 * 60, endMinute: 0, skill: 'a' }, // 16:00-24:00
    ],
    minRestMinutes: 0,
  };
  const r = schedule(input);
  assert.equal(r.status, 'infeasible');
  if (r.status !== 'infeasible') return;
  assert.equal(r.explanation.shifts.length, 2);
  assert.ok(r.explanation.rules.some((x) => x.includes('每日工时')));
});

test('跨日连续休息：夜班 06:00 下班后 11 小时内不能再上班', () => {
  const input: ScheduleInput = {
    employees: [{ id: 'E1', skills: ['n'], maxDailyMinutes: 600 }],
    shifts: [
      { id: 'night', day: 0, startMinute: 22 * 60, endMinute: 6 * 60, skill: 'n' },
      { id: 'morning', day: 1, startMinute: 7 * 60, endMinute: 15 * 60, skill: 'n' },
    ],
    minRestMinutes: 660,
  };
  const bad = schedule(input);
  assert.equal(bad.status, 'infeasible');
  if (bad.status === 'infeasible') {
    assert.equal(bad.explanation.shifts.length, 2);
    assert.ok(bad.explanation.rules.some((x) => x.includes('连续休息')));
  }
  // 休息要求放宽到 60 分钟：06:00 下班、07:00 上班恰好满足
  const ok = asSuccess(schedule({ ...input, minRestMinutes: 60 }));
  assert.equal(ok.assignments.length, 2);
});

test('连续夜班可行：06:00 下班到次日 22:00 间隔 16 小时', () => {
  const shifts = Array.from({ length: 7 }, (_, d) => ({
    id: `n${d}`,
    day: d,
    startMinute: 22 * 60,
    endMinute: 6 * 60,
    skill: 'n',
  }));
  const r = asSuccess(
    schedule({
      employees: [{ id: 'E1', skills: ['n'], maxDailyMinutes: 480 }],
      shifts,
      minRestMinutes: 660,
    }),
  );
  assert.equal(r.assignments.every((a) => a.employeeIds[0] === 'E1'), true);
});

test('不可用时间为硬约束', () => {
  const r = schedule({
    employees: [
      {
        id: 'E1',
        skills: ['a'],
        maxDailyMinutes: 600,
        unavailable: [{ day: 0, startMinute: 8 * 60, endMinute: 20 * 60 }],
      },
    ],
    shifts: [{ id: 's1', day: 0, startMinute: 9 * 60, endMinute: 17 * 60, skill: 'a' }],
    minRestMinutes: 0,
  });
  assert.equal(r.status, 'infeasible');
  if (r.status === 'infeasible') {
    assert.ok(r.explanation.rules.some((x) => x.includes('不可用')));
  }
});

test('多人需求班次按组合分配且互不重复', () => {
  const r = asSuccess(
    schedule({
      employees: [
        { id: 'E1', skills: ['a'], maxDailyMinutes: 600 },
        { id: 'E2', skills: ['a'], maxDailyMinutes: 600 },
        { id: 'E3', skills: ['a'], maxDailyMinutes: 600 },
      ],
      shifts: [{ id: 's1', day: 0, startMinute: 9 * 60, endMinute: 17 * 60, skill: 'a', required: 2 }],
      minRestMinutes: 0,
    }),
  );
  assert.equal(r.assignments[0]!.employeeIds.length, 2);
  assert.equal(new Set(r.assignments[0]!.employeeIds).size, 2);
});

test('超过 12 名员工直接报错', () => {
  const employees = Array.from({ length: 13 }, (_, i) => ({
    id: `E${i}`,
    skills: ['a'],
    maxDailyMinutes: 480,
  }));
  assert.throws(() => schedule({ employees, shifts: [], minRestMinutes: 0 }), /12/);
});

test('偏好目标必须存在', () => {
  assert.throws(
    () =>
      schedule({
        employees: [{ id: 'E1', skills: ['a'], maxDailyMinutes: 480 }],
        shifts: [{ id: 's1', day: 0, startMinute: 9 * 60, endMinute: 12 * 60, skill: 'a' }],
        minRestMinutes: 0,
        preferences: [{ employee: 'E1', type: 'prefer', weight: 1, shiftId: 'nope' }],
      }),
    /不存在的班次/,
  );
});

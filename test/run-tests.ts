import { Problem } from '../src/types';
import { shiftInterval } from '../src/time';
import { schedule, verify } from '../src/scheduler';
import { findMinimalInfeasibleCore } from '../src/core';

declare const process: { exitCode?: number };

let failures = 0;

function check(name: string, cond: boolean, detail?: string): void {
  if (cond) {
    console.log(`ok   - ${name}`);
  } else {
    failures++;
    console.log(`FAIL - ${name}${detail ? ` :: ${detail}` : ''}`);
  }
}

// 1. 夜班按真实起止分钟计算：22:00 -> 次日 06:00 = 480 分钟
{
  const iv = shiftInterval(0, 22 * 60, 6 * 60);
  check('夜班跨午夜时长为 480 分钟', iv.end - iv.start === 480, `got ${iv.end - iv.start}`);
  const iv2 = shiftInterval(6, 1320, 360);
  check('周日夜班延伸到第 8 天凌晨', iv2.end === 7 * 1440 + 360);
}

// 2. 偏好最小化：两名员工都能上，应选损失为 0 的那个
{
  const problem: Problem = {
    minRestMinutes: 600,
    employees: [
      { id: 'A', skills: ['收银'], maxDailyMinutes: 480, preferences: [{ shiftId: 's1', weight: 5 }] },
      { id: 'B', skills: ['收银'], maxDailyMinutes: 480 },
    ],
    shifts: [{ id: 's1', day: 0, start: 480, end: 960, skill: '收银' }],
  };
  const r = schedule(problem);
  check('偏好优化选择零损失员工', r.status === 'optimal' && r.totalPreferenceCost === 0
    && r.schedule[0].employeeId === 'B', JSON.stringify(r.schedule));
}

// 3. 技能约束强制选择有损失的员工
{
  const problem: Problem = {
    minRestMinutes: 600,
    employees: [
      { id: 'A', skills: ['收银'], maxDailyMinutes: 480, preferences: [{ shiftId: 's1', weight: 5 }] },
      { id: 'B', skills: ['保洁'], maxDailyMinutes: 480 },
    ],
    shifts: [{ id: 's1', day: 0, start: 480, end: 960, skill: '收银' }],
  };
  const r = schedule(problem);
  check('技能约束下承担最小损失 5', r.status === 'optimal' && r.totalPreferenceCost === 5
    && r.schedule[0].employeeId === 'A');
}

// 4. 跨日连续休息：晚班结束 24:00，次日 08:00 早班间隔 8h < 11h，不能同一人
{
  const problem: Problem = {
    minRestMinutes: 660,
    employees: [
      { id: 'A', skills: ['收银'], maxDailyMinutes: 600 },
      { id: 'B', skills: ['收银'], maxDailyMinutes: 600 },
    ],
    shifts: [
      { id: '晚', day: 0, start: 960, end: 0, skill: '收银' },
      { id: '早', day: 1, start: 480, end: 960, skill: '收银' },
    ],
  };
  const r = schedule(problem);
  const emps = new Set(r.schedule.map((e) => e.employeeId));
  check('跨日休息不足时拆给两人', r.status === 'optimal' && emps.size === 2,
    JSON.stringify(r.schedule));
  check('排班通过独立校验', verify(problem, r.schedule.map((e) => ({ shiftId: e.shiftId, employeeId: e.employeeId }))).length === 0);
}

// 5. 夜班后的休息：夜班 22:00-06:00，次日 08:00 间隔仅 2h
{
  const problem: Problem = {
    minRestMinutes: 660,
    employees: [{ id: 'A', skills: ['安保'], maxDailyMinutes: 720 }],
    shifts: [
      { id: '夜', day: 0, start: 1320, end: 360, skill: '安保' },
      { id: '早', day: 1, start: 480, end: 960, skill: '安保' },
    ],
  };
  const r = schedule(problem);
  check('夜班后 2 小时再上班不可行', r.status === 'infeasible');
  check('无解核心包含两个班次与 rest 规则',
    !!r.core && r.core.shifts.length === 2 && r.core.rules.includes('rest'),
    JSON.stringify(r.core));
}

// 6. 每日工时上限导致无解，核心应只剩一个班次 + dailyHours 规则
{
  const problem: Problem = {
    minRestMinutes: 0,
    employees: [{ id: 'A', skills: ['安保'], maxDailyMinutes: 480 }],
    shifts: [
      { id: 'n1', day: 0, start: 1320, end: 480, skill: '安保' },
      { id: 'n2', day: 1, start: 1320, end: 480, skill: '安保' },
      { id: 'n3', day: 2, start: 1320, end: 480, skill: '安保' },
    ],
  };
  const core = findMinimalInfeasibleCore(problem);
  check('工时长于上限时无解核心为单班次 + dailyHours',
    !!core && core.shifts.length === 1 && core.rules.length === 1 && core.rules[0] === 'dailyHours',
    JSON.stringify(core));
}

// 7. 多人需求（required > 1）与 12 人上限场景
{
  const employees = [];
  for (let i = 1; i <= 12; i++) {
    employees.push({ id: `E${i}`, skills: ['收银'], maxDailyMinutes: 480 });
  }
  const problem: Problem = {
    minRestMinutes: 600,
    employees,
    shifts: [
      { id: 's1', day: 0, start: 480, end: 960, skill: '收银', required: 3 },
      { id: 's2', day: 1, start: 480, end: 960, skill: '收银', required: 3 },
    ],
  };
  const r = schedule(problem);
  check('12 人 / 多人需求场景可解且最优', r.status === 'optimal' && r.schedule.length === 6);
  check('多人需求排班通过独立校验',
    verify(problem, r.schedule.map((e) => ({ shiftId: e.shiftId, employeeId: e.employeeId }))).length === 0);
}

// 8. 员工超过 12 人会被拒绝
{
  const employees = [];
  for (let i = 1; i <= 13; i++) {
    employees.push({ id: `E${i}`, skills: ['收银'], maxDailyMinutes: 480 });
  }
  const problem: Problem = { minRestMinutes: 0, employees, shifts: [] };
  const r = schedule(problem);
  check('超过 12 名员工返回 invalid', r.status === 'invalid');
}

// 9. 不可用时段（硬约束）
{
  const problem: Problem = {
    minRestMinutes: 0,
    employees: [
      { id: 'A', skills: ['收银'], maxDailyMinutes: 480, unavailable: [{ day: 0, start: 480, end: 960 }] },
      { id: 'B', skills: ['收银'], maxDailyMinutes: 480 },
    ],
    shifts: [{ id: 's1', day: 0, start: 480, end: 960, skill: '收银' }],
  };
  const r = schedule(problem);
  check('不可用时段被跳过', r.status === 'optimal' && r.schedule[0].employeeId === 'B');
}

// 10. 时间窗偏好（跨午夜窗口匹配夜班）
{
  const problem: Problem = {
    minRestMinutes: 0,
    employees: [
      { id: 'A', skills: ['安保'], maxDailyMinutes: 720, preferences: [{ start: 1200, end: 360, weight: 4 }] },
      { id: 'B', skills: ['安保'], maxDailyMinutes: 720 },
    ],
    shifts: [{ id: '夜', day: 0, start: 1320, end: 360, skill: '安保' }],
  };
  const r = schedule(problem);
  check('跨午夜时间窗偏好生效', r.status === 'optimal' && r.schedule[0].employeeId === 'B'
    && r.totalPreferenceCost === 0);
}

if (failures > 0) {
  console.log(`\n${failures} 个测试失败`);
  process.exitCode = 1;
} else {
  console.log('\n全部测试通过');
}

import { schedule } from './scheduler.ts';
import { renderExplanation, renderSchedule } from './format.ts';
import type { ScheduleInput, ShiftInput } from './types.ts';

// ---------- 场景一：8 名员工、每天 5 个班次（含跨夜 22:00-06:00），可行 ----------

const employees = [
  { id: '王芳', skills: ['cashier', 'floor'], maxDailyMinutes: 480 },
  { id: '李伟', skills: ['cashier'], maxDailyMinutes: 480 },
  { id: '张敏', skills: ['floor'], maxDailyMinutes: 480 },
  { id: '陈杰', skills: ['cashier', 'floor', 'night'], maxDailyMinutes: 480 },
  { id: '刘洋', skills: ['floor', 'night'], maxDailyMinutes: 480 },
  { id: '赵磊', skills: ['cashier', 'night'], maxDailyMinutes: 480 },
  {
    id: '孙丽',
    skills: ['floor'],
    maxDailyMinutes: 480,
    unavailable: [{ day: 2, startMinute: 0, endMinute: 12 * 60 }], // 周三上午不可用
  },
  { id: '周强', skills: ['cashier', 'floor'], maxDailyMinutes: 480 },
];

const shifts: ShiftInput[] = [];
for (let day = 0; day < 7; day++) {
  shifts.push(
    { id: `d${day}-am-cash`, day, startMinute: 8 * 60, endMinute: 16 * 60, skill: 'cashier' },
    { id: `d${day}-am-floor`, day, startMinute: 8 * 60, endMinute: 16 * 60, skill: 'floor' },
    { id: `d${day}-pm-cash`, day, startMinute: 14 * 60, endMinute: 22 * 60, skill: 'cashier' },
    { id: `d${day}-pm-floor`, day, startMinute: 14 * 60, endMinute: 22 * 60, skill: 'floor' },
    { id: `d${day}-night`, day, startMinute: 22 * 60, endMinute: 6 * 60, skill: 'night' }, // 跨夜
  );
}

const feasibleInput: ScheduleInput = {
  employees,
  shifts,
  minRestMinutes: 660, // 班间至少连续休息 11 小时
  preferences: [
    { employee: '王芳', type: 'prefer', weight: 5, shiftId: 'd0-am-cash' },
    { employee: '赵磊', type: 'avoid', weight: 4, shiftId: 'd5-night' },
    { employee: '陈杰', type: 'avoid', weight: 3, day: 6 },
    { employee: '孙丽', type: 'prefer', weight: 2, day: 4 },
    { employee: '周强', type: 'avoid', weight: 2, day: 1, startMinute: 14 * 60, endMinute: 22 * 60 },
  ],
};

console.log('========== 场景一：可行排班 ==========');
const r1 = schedule(feasibleInput);
console.log(r1.status === 'optimal' || r1.status === 'feasible' ? renderSchedule(feasibleInput, r1) : JSON.stringify(r1));

// ---------- 场景二：夜班后清晨接着排班，违反 11 小时连续休息，不可解 ----------

const infeasibleInput: ScheduleInput = {
  employees: [
    { id: '刘洋', skills: ['night'], maxDailyMinutes: 480 },
    { id: '孙丽', skills: ['floor'], maxDailyMinutes: 480 },
  ],
  shifts: [
    { id: 'n0', day: 0, startMinute: 22 * 60, endMinute: 6 * 60, skill: 'night' }, // 周一 22:00 - 周二 06:00
    { id: 'm1', day: 1, startMinute: 7 * 60, endMinute: 15 * 60, skill: 'night' }, // 周二 07:00 - 15:00
  ],
  minRestMinutes: 660,
};

console.log('\n========== 场景二：不可解（输出最小班次与规则集合） ==========');
const r2 = schedule(infeasibleInput);
console.log(r2.status === 'infeasible' ? renderExplanation(r2.explanation) : JSON.stringify(r2));

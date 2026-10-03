import type { Trajectory, Zone } from './types.ts';

/** 场景基准时间：2026-01-15 09:00:00 +08:00 */
export const BASE = Date.parse('2026-01-15T09:00:00+08:00');
const MIN = 60_000;
const SEC = 1000;

/** 商场柜台/区域（坐标单位：米，原点为商场中庭）。 */
export const MALL_ZONES: Zone[] = [
  { name: '咖啡柜台', x: 30, y: 8, floor: 1, radius: 12 },
  { name: '生鲜柜台', x: -25, y: 15, floor: 1, radius: 12 },
  { name: '收银台A', x: 0, y: 0, floor: 1, radius: 12 },
  { name: '服装柜台', x: 10, y: -20, floor: 1, radius: 12 },
];

/** 确诊者甲：停车场 -> 咖啡柜台 -> 生鲜柜台 -> 收银台 -> 离开。 */
export const CASE_A: Trajectory = {
  entityId: '确诊者甲',
  points: [
    { t: BASE + 0 * MIN, x: -60, y: 40, floor: 1, label: '停车场入口' },
    { t: BASE + 14 * MIN, x: 30, y: 8, floor: 1 },
    { t: BASE + 20 * MIN, x: 30, y: 8, floor: 1, label: '咖啡柜台' },
    { t: BASE + 65 * MIN, x: -25, y: 15, floor: 1 },
    { t: BASE + 70 * MIN, x: -25, y: 15, floor: 1, label: '生鲜柜台' },
    { t: BASE + 145 * MIN, x: 0, y: 0, floor: 1 },
    { t: BASE + 150 * MIN, x: 0, y: 0, floor: 1, label: '收银台A' },
    { t: BASE + 165 * MIN, x: -60, y: 40, floor: 1, label: '离开商场' },
  ],
};

/** 确诊者乙：三次与甲同处同一柜台，其余时间远离。 */
export const CASE_B: Trajectory = {
  entityId: '确诊者乙',
  points: [
    { t: BASE + 16 * MIN, x: 200, y: 300, floor: 1, label: '商场外' },
    { t: BASE + 17 * MIN + 30 * SEC, x: 30, y: 8, floor: 1 },
    { t: BASE + 22 * MIN, x: 30, y: 8, floor: 1, label: '咖啡柜台' },
    { t: BASE + 35 * MIN, x: 500, y: 500, floor: 1, label: '商场外' },
    { t: BASE + 60 * MIN, x: 500, y: 500, floor: 1 },
    { t: BASE + 67 * MIN, x: -25, y: 15, floor: 1 },
    { t: BASE + 72 * MIN, x: -25, y: 15, floor: 1, label: '生鲜柜台' },
    { t: BASE + 90 * MIN, x: 500, y: 500, floor: 1, label: '商场外' },
    { t: BASE + 140 * MIN, x: 500, y: 500, floor: 1 },
    { t: BASE + 148 * MIN, x: 0, y: 0, floor: 1 },
    { t: BASE + 153 * MIN, x: 0, y: 0, floor: 1, label: '收银台A' },
    { t: BASE + 170 * MIN, x: 500, y: 500, floor: 1, label: '商场外' },
  ],
};

export const DAYS = 7;
export const MINUTES_PER_DAY = 1440;
export const MAX_EMPLOYEES = 12;

/** 一天内的不可用窗口；endMinute <= startMinute 表示跨到次日（如 22:00-06:00）。 */
export interface UnavailableWindow {
  day: number; // 0..6
  startMinute: number; // 0..1439，当天 00:00 起的分钟数
  endMinute: number; // 0..1439
}

/** 一个班次；endMinute <= startMinute 表示夜班跨日，按真实起止分钟换算。 */
export interface ShiftInput {
  id: string;
  day: number; // 0..6
  startMinute: number;
  endMinute: number;
  skill: string;
  required?: number; // 需求人数，默认 1
}

export interface EmployeeInput {
  id: string;
  skills: string[];
  maxDailyMinutes: number; // 硬约束：每日工时上限
  unavailable?: UnavailableWindow[]; // 硬约束：不可用时间
}

/**
 * 带权偏好。目标二选一：
 *  - shiftId：某个具体班次；
 *  - day（可再叠加 startMinute/endMinute 时间窗）：当天（时间窗内）的全部班次。
 * prefer：未分配到目标班次 -> 损失 weight；avoid：被分配到目标班次 -> 损失 weight。
 */
export interface PreferenceInput {
  employee: string;
  type: 'prefer' | 'avoid';
  weight: number;
  shiftId?: string;
  day?: number;
  startMinute?: number;
  endMinute?: number;
}

export interface ScheduleInput {
  employees: EmployeeInput[]; // 1..12 人
  shifts: ShiftInput[];
  minRestMinutes: number; // 硬约束：同一员工相邻班次间的最小连续休息分钟数（跨日生效）
  preferences?: PreferenceInput[];
}

export interface SolveOptions {
  nodeBudget?: number; // 搜索节点预算，默认 2_000_000
  timeBudgetMs?: number; // 时间预算，默认 10_000
}

export interface ShiftAssignment {
  shiftId: string;
  employeeIds: string[];
}

export interface PreferenceLossEntry {
  employee: string;
  type: 'prefer' | 'avoid';
  weight: number;
  target: string;
}

export interface ScheduleSuccess {
  status: 'optimal' | 'feasible'; // feasible = 找到解但预算耗尽，未证明最优
  assignments: ShiftAssignment[];
  totalLoss: number;
  losses: PreferenceLossEntry[];
  stats: { nodes: number };
}

export interface InfeasibilityExplanation {
  shifts: string[]; // 最小不可解集合中的班次（人类可读描述）
  rules: string[]; // 最小不可解集合中的规则（人类可读描述）
  summary: string;
}

export interface ScheduleInfeasible {
  status: 'infeasible';
  explanation: InfeasibilityExplanation;
}

export interface ScheduleUnknown {
  status: 'unknown';
  reason: string;
}

export type ScheduleResult = ScheduleSuccess | ScheduleInfeasible | ScheduleUnknown;

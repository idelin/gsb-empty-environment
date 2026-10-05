/** 一天中的分钟数 */
export const MINUTES_PER_DAY = 1440;

/** 一周内的一天，0 = 周一 … 6 = 周日 */
export type DayIndex = number;

/**
 * 一个班次定义。
 * start / end 为当天零点起的分钟数（0–1439）。
 * 当 end <= start 时视为跨午夜班次（夜班），实际结束时间为次日 end。
 */
export interface ShiftDef {
  id: string;
  day: DayIndex;
  start: number;
  end: number;
  skill: string;
  /** 需要人数，默认 1 */
  required?: number;
}

/** 员工不可用时段（硬约束），start/end 语义与班次相同，支持跨午夜 */
export interface TimeWindow {
  day: DayIndex;
  start: number;
  end: number;
}

/**
 * 带权偏好：当员工被分配到“匹配”的班次时，产生 weight 的损失。
 * 匹配规则：所有填写的字段都必须匹配。
 *  - shiftId：指定班次 id
 *  - day：指定星期几
 *  - start/end：班次开始分钟落在该时间窗内（end <= start 时窗口跨午夜）
 */
export interface Preference {
  shiftId?: string;
  day?: DayIndex;
  start?: number;
  end?: number;
  weight: number;
}

export interface Employee {
  id: string;
  skills: string[];
  /** 每日最大工时（分钟） */
  maxDailyMinutes: number;
  unavailable?: TimeWindow[];
  preferences?: Preference[];
}

export interface Problem {
  /** 同一员工相邻两个班次之间必须保证的最小连续休息分钟数（跨日按真实时间轴计算） */
  minRestMinutes: number;
  /** 最多 12 人 */
  employees: Employee[];
  /** 7 天内的班次 */
  shifts: ShiftDef[];
}

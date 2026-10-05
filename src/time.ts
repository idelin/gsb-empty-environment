import { MINUTES_PER_DAY } from './types';

/** 绝对时间轴上的区间（从第 0 天 00:00 起的分钟数） */
export interface AbsInterval {
  start: number;
  end: number;
}

/**
 * 把 (day, start, end) 转换为绝对时间轴区间。
 * end <= start 视为跨午夜（夜班），结束时间加一天。
 */
export function shiftInterval(day: number, start: number, end: number): AbsInterval {
  const absStart = day * MINUTES_PER_DAY + start;
  const absEnd = day * MINUTES_PER_DAY + (end <= start ? end + MINUTES_PER_DAY : end);
  return { start: absStart, end: absEnd };
}

export function overlaps(a: AbsInterval, b: AbsInterval): boolean {
  return a.start < b.end && b.start < a.end;
}

/** 两个不重叠区间之间的间隔分钟数 */
export function gapBetween(a: AbsInterval, b: AbsInterval): number {
  return a.start >= b.end ? a.start - b.end : b.start - a.end;
}

/** 分钟数格式化为 HH:MM */
export function formatMinutes(m: number): string {
  const hh = Math.floor(m / 60);
  const mm = m % 60;
  const pad = (n: number) => (n < 10 ? '0' + n : String(n));
  return pad(hh) + ':' + pad(mm);
}

export const DAY_NAMES = ['周一', '周二', '周三', '周四', '周五', '周六', '周日'] as const;

/** 分钟数格式化为 HH:MM（对 1440 取模，支持跨日绝对分钟）。 */
export function hhmm(minute: number): string {
  const m = ((minute % 1440) + 1440) % 1440;
  const h = Math.floor(m / 60);
  const mm = m % 60;
  return `${String(h).padStart(2, '0')}:${String(mm).padStart(2, '0')}`;
}

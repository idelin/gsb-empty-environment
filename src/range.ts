import type { Range } from './types.ts';

/** 判断整数版本 v 是否落在闭区间 r 内。 */
export function inRange(v: number, r: Range): boolean {
  if (r.min !== undefined && v < r.min) return false;
  if (r.max !== undefined && v > r.max) return false;
  return true;
}

/** 求两个闭区间的交集；交集为空时返回 null。 */
export function intersectRange(a: Range, b: Range): Range | null {
  const min = Math.max(a.min ?? Number.NEGATIVE_INFINITY, b.min ?? Number.NEGATIVE_INFINITY);
  const max = Math.min(a.max ?? Number.POSITIVE_INFINITY, b.max ?? Number.POSITIVE_INFINITY);
  if (min > max) return null;
  const out: Range = {};
  if (Number.isFinite(min)) out.min = min;
  if (Number.isFinite(max)) out.max = max;
  return out;
}

/** 把区间格式化为人类可读形式，如 `>=1 <=3` 或 `*`。 */
export function formatRange(r: Range | undefined): string {
  if (r === undefined || (r.min === undefined && r.max === undefined)) return '*';
  const parts: string[] = [];
  if (r.min !== undefined) parts.push(`>=${r.min}`);
  if (r.max !== undefined) parts.push(`<=${r.max}`);
  return parts.join(' ');
}

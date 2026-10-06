/**
 * UTC 工作日历库：在半开区间 [start, end) 表示的开放时间（epoch 毫秒）上累加工作时长。
 *
 * 语义约定：
 * - 开放区间可重叠、可乱序，使用前先做并集。
 * - 暂停日（UTC 日期，'YYYY-MM-DD'）覆盖并删除当天全部开放时间。
 * - 时长为正向前累加，为负向后倒推；恰好落在区间端点上视为合法完成。
 * - 返回值携带经过的区间证据（segments），按时间先后排列。
 */

export interface Interval {
  /** epoch 毫秒，含 */
  start: number;
  /** epoch 毫秒，不含 */
  end: number;
}

export interface AddWorkResult {
  /** 完成时刻（epoch 毫秒） */
  end: number;
  /** 经过的开放区间证据，按时间先后排列，互不重叠 */
  segments: Interval[];
}

const DAY_MS = 86_400_000;

/** 合并重叠或相邻的区间，返回按 start 升序的并集。 */
export function unionIntervals(intervals: readonly Interval[]): Interval[] {
  const sorted = intervals
    .filter((iv) => Number.isFinite(iv.start) && Number.isFinite(iv.end) && iv.end > iv.start)
    .map((iv) => ({ start: iv.start, end: iv.end }))
    .sort((a, b) => a.start - b.start || a.end - b.end);

  const merged: Interval[] = [];
  for (const iv of sorted) {
    const last = merged[merged.length - 1];
    if (last && iv.start <= last.end) {
      if (iv.end > last.end) last.end = iv.end;
    } else {
      merged.push({ start: iv.start, end: iv.end });
    }
  }
  return merged;
}

/** 解析 'YYYY-MM-DD' 为当天 UTC 半开区间；非法日期抛错。 */
export function pauseDayRange(day: string): Interval {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(day);
  if (!m) throw new Error(`invalid pause day: ${JSON.stringify(day)}`);
  const year = Number(m[1]);
  const month = Number(m[2]);
  const date = Number(m[3]);
  const start = Date.UTC(year, month - 1, date);
  const d = new Date(start);
  if (
    d.getUTCFullYear() !== year ||
    d.getUTCMonth() !== month - 1 ||
    d.getUTCDate() !== date
  ) {
    throw new Error(`invalid pause day: ${JSON.stringify(day)}`);
  }
  return { start, end: start + DAY_MS };
}

/** 从已排序互不重叠的区间列表中删除一个区间。 */
function subtractOne(intervals: readonly Interval[], cut: Interval): Interval[] {
  const out: Interval[] = [];
  for (const iv of intervals) {
    if (cut.end <= iv.start || cut.start >= iv.end) {
      out.push(iv);
      continue;
    }
    if (iv.start < cut.start) out.push({ start: iv.start, end: cut.start });
    if (cut.end < iv.end) out.push({ start: cut.end, end: iv.end });
  }
  return out;
}

/** 暂停日覆盖当天全部开放时间：从开放区间中删去每个暂停日的 UTC 全天。 */
export function applyPauseDays(
  intervals: readonly Interval[],
  pauseDays: readonly string[],
): Interval[] {
  let result = intervals.slice();
  for (const day of pauseDays) {
    result = subtractOne(result, pauseDayRange(day));
  }
  return result;
}

/** 计算有效日历：并集 + 暂停日删除。 */
export function effectiveCalendar(
  open: readonly Interval[],
  pauseDays: readonly string[] = [],
): Interval[] {
  return applyPauseDays(unionIntervals(open), pauseDays);
}

function walkForward(start: number, remaining: number, cal: readonly Interval[]): AddWorkResult {
  const segments: Interval[] = [];
  let t = start;
  for (const iv of cal) {
    if (iv.end <= t) continue;
    const from = Math.max(t, iv.start);
    const avail = iv.end - from;
    if (remaining <= avail) {
      if (remaining > 0) segments.push({ start: from, end: from + remaining });
      return { end: from + remaining, segments };
    }
    if (avail > 0) segments.push({ start: from, end: iv.end });
    remaining -= avail;
    t = iv.end;
  }
  throw new RangeError(`insufficient calendar: ${remaining}ms of work time left uncovered`);
}

function walkBackward(start: number, remaining: number, cal: readonly Interval[]): AddWorkResult {
  const segments: Interval[] = [];
  let t = start;
  for (let i = cal.length - 1; i >= 0; i--) {
    const iv = cal[i];
    if (iv.start >= t) continue;
    const to = Math.min(t, iv.end);
    const avail = to - iv.start;
    if (remaining <= avail) {
      if (remaining > 0) segments.push({ start: to - remaining, end: to });
      segments.reverse();
      return { end: to - remaining, segments };
    }
    if (avail > 0) segments.push({ start: iv.start, end: to });
    remaining -= avail;
    t = iv.start;
  }
  throw new RangeError(`insufficient calendar: ${remaining}ms of work time left uncovered`);
}

/**
 * 在开放时间上累加工作时长。
 *
 * @param start 起始时刻（epoch 毫秒），本身不需要落在开放时间内
 * @param durationMs 工作时长（毫秒）；负数表示从 start 向后倒推
 * @param open 开放区间（可重叠、可乱序），半开区间 [start, end)
 * @param pauseDays 暂停日（UTC 'YYYY-MM-DD'），覆盖当天全部开放时间
 * @throws RangeError 日历中可用开放时间不足
 */
export function addWorkTime(
  start: number,
  durationMs: number,
  open: readonly Interval[],
  pauseDays: readonly string[] = [],
): AddWorkResult {
  if (!Number.isFinite(start)) throw new TypeError("start must be a finite number");
  if (!Number.isFinite(durationMs)) throw new TypeError("durationMs must be a finite number");
  const cal = effectiveCalendar(open, pauseDays);
  if (durationMs === 0) return { end: start, segments: [] };
  return durationMs > 0
    ? walkForward(start, durationMs, cal)
    : walkBackward(start, -durationMs, cal);
}

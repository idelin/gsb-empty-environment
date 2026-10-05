const DAY_MS = 86_400_000;
const DATE_RE = /^(\d{4})-(\d{2})-(\d{2})$/;

/** YYYY-MM-DD -> UTC 纪元日序号，非法日期直接抛错。 */
export function toDayIndex(date: string): number {
  const m = DATE_RE.exec(date);
  if (!m) throw new Error(`非法日期格式（应为 YYYY-MM-DD）: ${date}`);
  const y = Number(m[1]);
  const mo = Number(m[2]);
  const d = Number(m[3]);
  const t = Date.UTC(y, mo - 1, d);
  const dt = new Date(t);
  if (dt.getUTCFullYear() !== y || dt.getUTCMonth() !== mo - 1 || dt.getUTCDate() !== d) {
    throw new Error(`不存在的日期: ${date}`);
  }
  return t / DAY_MS;
}

export function toDateString(dayIndex: number): string {
  return new Date(dayIndex * DAY_MS).toISOString().slice(0, 10);
}

/** 半开区间 [from, to) 内的全部自然日。 */
export function listDays(from: string, to: string): string[] {
  const start = toDayIndex(from);
  const end = toDayIndex(to);
  if (start >= end) throw new Error(`日期区间为空: [${from}, ${to})`);
  const out: string[] = [];
  for (let i = start; i < end; i++) out.push(toDateString(i));
  return out;
}

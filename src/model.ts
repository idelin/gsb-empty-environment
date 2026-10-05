import type { PreferenceInput, ScheduleInput } from './types.ts';
import { DAYS, MAX_EMPLOYEES, MINUTES_PER_DAY } from './types.ts';
import { hhmm } from './util.ts';

/** 技能要求被放松时使用的通配技能（仅用于不可解解释）。 */
export const ANY_SKILL = '*';

export interface Shift {
  id: string;
  index: number;
  day: number; // 起始日 0..6
  start: number; // 周内绝对分钟
  end: number; // 周内绝对分钟，end > start（跨日已展开）
  duration: number;
  skill: string;
  required: number;
}

export interface Employee {
  id: string;
  index: number;
  skills: ReadonlySet<string>;
  maxDailyMinutes: number;
}

export interface CompiledPref {
  emp: number;
  type: 'prefer' | 'avoid';
  weight: number;
  shifts: boolean[]; // 命中的班次集合
  target: string; // 人类可读的目标描述
}

export interface Model {
  shifts: Shift[];
  employees: Employee[];
  prefs: CompiledPref[];
  minRest: number;
  candidate: boolean[][]; // [employee][shift] 技能 + 不可用时间预过滤
}

/** 把「第 day 天 startMinute-endMinute」换算成周内绝对分钟区间；endMinute <= startMinute 视为跨日。 */
export function absInterval(day: number, startMinute: number, endMinute: number): [number, number] {
  const start = day * MINUTES_PER_DAY + startMinute;
  let end = day * MINUTES_PER_DAY + endMinute;
  if (endMinute <= startMinute) end += MINUTES_PER_DAY;
  return [start, end];
}

const overlaps = (a0: number, a1: number, b0: number, b1: number): boolean => a0 < b1 && b0 < a1;

function fail(message: string): never {
  throw new Error(`排班输入非法: ${message}`);
}

function checkMinute(value: number, what: string): void {
  if (!Number.isInteger(value) || value < 0 || value >= MINUTES_PER_DAY) {
    fail(`${what} 必须是 0..1439 的整数分钟，得到 ${value}`);
  }
}

function checkDay(value: number, what: string): void {
  if (!Number.isInteger(value) || value < 0 || value >= DAYS) {
    fail(`${what} 必须是 0..6 的整数，得到 ${value}`);
  }
}

function prefTargetDescription(p: PreferenceInput): string {
  if (p.shiftId !== undefined) return `班次 ${p.shiftId}`;
  let text = `第 ${(p.day ?? 0) + 1} 天`;
  if (p.startMinute !== undefined) text += ` ${hhmm(p.startMinute)}-${hhmm(p.endMinute ?? 0)}`;
  return text;
}

export function buildModel(input: ScheduleInput): Model {
  if (!Array.isArray(input.employees) || input.employees.length === 0) fail('至少需要 1 名员工');
  if (input.employees.length > MAX_EMPLOYEES) fail(`员工数 ${input.employees.length} 超过上限 ${MAX_EMPLOYEES}`);
  if (!Number.isInteger(input.minRestMinutes) || input.minRestMinutes < 0) fail('minRestMinutes 必须是非负整数');

  const empIndex = new Map<string, number>();
  input.employees.forEach((e, i) => {
    if (empIndex.has(e.id)) fail(`员工 id 重复: ${e.id}`);
    empIndex.set(e.id, i);
    if (!(e.maxDailyMinutes > 0)) fail(`员工 ${e.id} 的 maxDailyMinutes 必须为正数`);
    for (const w of e.unavailable ?? []) {
      checkDay(w.day, `员工 ${e.id} 不可用窗口的 day`);
      checkMinute(w.startMinute, `员工 ${e.id} 不可用窗口的 startMinute`);
      checkMinute(w.endMinute, `员工 ${e.id} 不可用窗口的 endMinute`);
      if (w.startMinute === w.endMinute) fail(`员工 ${e.id} 的不可用窗口起止相同`);
    }
  });

  const shiftIndex = new Map<string, number>();
  input.shifts.forEach((s) => {
    if (shiftIndex.has(s.id)) fail(`班次 id 重复: ${s.id}`);
    shiftIndex.set(s.id, shiftIndex.size);
    checkDay(s.day, `班次 ${s.id} 的 day`);
    checkMinute(s.startMinute, `班次 ${s.id} 的 startMinute`);
    checkMinute(s.endMinute, `班次 ${s.id} 的 endMinute`);
    if (s.startMinute === s.endMinute) fail(`班次 ${s.id} 起止时间相同（如需 24 小时班请拆段）`);
    if (!s.skill) fail(`班次 ${s.id} 缺少技能要求`);
    const required = s.required ?? 1;
    if (!Number.isInteger(required) || required < 1) fail(`班次 ${s.id} 的 required 必须是正整数`);
  });

  const shifts: Shift[] = input.shifts.map((s, i) => {
    const [start, end] = absInterval(s.day, s.startMinute, s.endMinute);
    return {
      id: s.id,
      index: i,
      day: s.day,
      start,
      end,
      duration: end - start,
      skill: s.skill,
      required: s.required ?? 1,
    };
  });

  const employees: Employee[] = input.employees.map((e, i) => ({
    id: e.id,
    index: i,
    skills: new Set(e.skills),
    maxDailyMinutes: e.maxDailyMinutes,
  }));

  const unavailable = input.employees.map((e) =>
    (e.unavailable ?? []).map((w) => absInterval(w.day, w.startMinute, w.endMinute)),
  );

  const candidate: boolean[][] = employees.map((emp, e) =>
    shifts.map((sh) => {
      if (sh.skill !== ANY_SKILL && !emp.skills.has(sh.skill)) return false;
      return !unavailable[e]!.some(([u0, u1]) => overlaps(u0, u1, sh.start, sh.end));
    }),
  );

  const prefs: CompiledPref[] = (input.preferences ?? []).map((p) => {
    const emp = empIndex.get(p.employee);
    if (emp === undefined) fail(`偏好引用了不存在的员工: ${p.employee}`);
    if (!(p.weight > 0)) fail(`员工 ${p.employee} 的偏好权重必须为正数`);
    if (p.type !== 'prefer' && p.type !== 'avoid') fail(`偏好类型必须是 prefer 或 avoid`);
    const hits = new Array<boolean>(shifts.length).fill(false);
    if (p.shiftId !== undefined) {
      const s = shiftIndex.get(p.shiftId);
      if (s === undefined) fail(`偏好引用了不存在的班次: ${p.shiftId}`);
      hits[s] = true;
    } else if (p.day !== undefined) {
      checkDay(p.day, `员工 ${p.employee} 偏好的 day`);
      let window: [number, number] | undefined;
      if (p.startMinute !== undefined || p.endMinute !== undefined) {
        if (p.startMinute === undefined || p.endMinute === undefined) fail('偏好时间窗必须同时给出 startMinute 和 endMinute');
        checkMinute(p.startMinute, '偏好时间窗 startMinute');
        checkMinute(p.endMinute, '偏好时间窗 endMinute');
        window = absInterval(p.day, p.startMinute, p.endMinute);
      }
      shifts.forEach((sh, s) => {
        if (sh.day !== p.day) return;
        if (window && !overlaps(sh.start, sh.end, window[0], window[1])) return;
        hits[s] = true;
      });
    } else {
      fail(`员工 ${p.employee} 的偏好必须指定 shiftId 或 day`);
    }
    if (!hits.some(Boolean)) fail(`员工 ${p.employee} 的偏好没有匹配到任何班次`);
    return { emp, type: p.type, weight: p.weight, shifts: hits, target: prefTargetDescription(p) };
  });

  return { shifts, employees, prefs, minRest: input.minRestMinutes, candidate };
}

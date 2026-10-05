import type {
  InfeasibilityExplanation,
  ScheduleInput,
  ScheduleSuccess,
} from './types.ts';
import { DAY_NAMES, hhmm } from './util.ts';

function shiftTimeLabel(startMinute: number, endMinute: number): string {
  const overnight = endMinute <= startMinute;
  const end = overnight && endMinute !== 0 ? `次日${hhmm(endMinute)}` : hhmm(endMinute);
  return `${hhmm(startMinute)}-${end}`;
}

export function renderSchedule(input: ScheduleInput, result: ScheduleSuccess): string {
  const byId = new Map(result.assignments.map((a) => [a.shiftId, a.employeeIds]));
  const lines: string[] = [];
  lines.push(
    `状态: ${result.status === 'optimal' ? '最优解' : '可行解（未证明最优）'}，` +
      `偏好总损失 = ${result.totalLoss}，搜索节点 = ${result.stats.nodes}`,
  );
  for (let day = 0; day < 7; day++) {
    lines.push(`第 ${day + 1} 天（${DAY_NAMES[day]}）`);
    const todays = input.shifts
      .filter((s) => s.day === day)
      .sort((a, b) => a.startMinute - b.startMinute);
    if (todays.length === 0) {
      lines.push('  （无班次）');
      continue;
    }
    for (const s of todays) {
      const who = (byId.get(s.id) ?? []).join('、') || '（未排）';
      const time = shiftTimeLabel(s.startMinute, s.endMinute);
      lines.push(`  ${s.id}  ${time}  [${s.skill}] ×${s.required ?? 1}  →  ${who}`);
    }
  }
  if (result.losses.length > 0) {
    lines.push('未满足的偏好:');
    for (const l of result.losses) {
      const verb = l.type === 'prefer' ? '希望分配到' : '希望避开';
      lines.push(`  - ${l.employee} ${verb}${l.target}（权重 ${l.weight}）`);
    }
  } else {
    lines.push('全部偏好均已满足。');
  }
  return lines.join('\n');
}

export function renderExplanation(explanation: InfeasibilityExplanation): string {
  const lines: string[] = ['无法排班。导致无解的最小班次与规则集合:'];
  lines.push('班次:');
  for (const s of explanation.shifts) lines.push(`  - ${s}`);
  if (explanation.shifts.length === 0) lines.push('  （无）');
  lines.push('规则:');
  for (const r of explanation.rules) lines.push(`  - ${r}`);
  if (explanation.rules.length === 0) lines.push('  （无）');
  lines.push(explanation.summary);
  return lines.join('\n');
}

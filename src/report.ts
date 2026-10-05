import { Plan, RefStatus } from './types';

const STATUS_LABEL: Record<RefStatus, string> = {
  unchanged: '不变',
  redirected: '重定向',
  broken: '断裂',
  fixed: '修复',
  'broken-both': '前后均断裂',
};

const REASON_LABEL: Record<string, string> = {
  target: '目标分组',
  'cycle-break': '破环',
};

export function formatPlan(plan: Plan): string {
  const lines: string[] = [];
  lines.push(`=== 迁移计划: ${plan.scenario} ===`);
  lines.push(`状态: ${plan.feasible ? '可行' : '不可行'}`);
  if (plan.problems.length > 0) {
    lines.push('问题:');
    for (const problem of plan.problems) lines.push(`  - ${problem}`);
    return lines.join('\n');
  }

  lines.push(`移动 (${plan.moveCount} 个节点):`);
  if (plan.moveCount === 0) {
    lines.push('  (无需移动)');
  }
  for (const move of plan.moves) {
    lines.push(`  - ${move.id}: ${move.from ?? '<root>'} -> ${move.to}  [${REASON_LABEL[move.reason]}]`);
  }

  lines.push(`权限例外 (${plan.exceptionCount} 条):`);
  if (plan.exceptionCount === 0) {
    lines.push('  (无需例外)');
  }
  for (const entry of plan.exceptions) {
    const sign = entry.kind === 'grant' ? '+' : '-';
    const label = entry.kind === 'grant' ? '补授' : '回收';
    lines.push(`  - ${entry.node}: ${sign}${entry.permission}  [${label}]`);
  }

  lines.push(`引用解析 (${plan.refResolutions.length} 条, 迁移前 -> 迁移后):`);
  if (plan.refResolutions.length === 0) {
    lines.push('  (无引用)');
  }
  for (const ref of plan.refResolutions) {
    const before = ref.beforePath ? `${ref.beforePath} [${ref.beforeId}]` : 'BROKEN';
    const after = ref.afterPath ? `${ref.afterPath} [${ref.afterId}]` : 'BROKEN';
    lines.push(`  - ${ref.owner}  "${ref.ref}": ${before} -> ${after}  (${STATUS_LABEL[ref.status]})`);
  }

  lines.push(
    `身份校验: ${plan.identityPreserved ? '通过 —— 节点集合迁移前后一致，仅移动、未复制' : '失败 —— 节点身份不一致'}`,
  );
  return lines.join('\n');
}

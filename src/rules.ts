import type { NewStatement, OldStatement } from './types.ts';

/**
 * 显式映射规则。只有规则声明且通过校验的对应关系才会继承覆盖，
 * 工具绝不按行号平移来猜测对应关系。
 *
 * - identity: 一对一，且要求两侧内容指纹一致（用于 id 变更但内容未变）。
 * - rename:   一对一，人工确认内容虽变但语义同一（必须给出 reason）。
 * - split:    一对多，旧语句拆成多条新语句。
 * - merge:    多对一，多条旧语句合并成一条新语句，来源分别保留。
 */
export type MappingRule =
  | { kind: 'identity'; oldId: string; newId: string }
  | { kind: 'rename'; oldId: string; newId: string; reason: string }
  | { kind: 'split'; oldId: string; newIds: string[]; reason: string }
  | { kind: 'merge'; oldIds: string[]; newId: string; reason: string };

export interface RulesFile {
  version: 1;
  rules: MappingRule[];
}

export interface RuleRejection {
  rule: MappingRule;
  reason: string;
}

function ruleOldIds(rule: MappingRule): string[] {
  return rule.kind === 'merge' ? rule.oldIds : [rule.oldId];
}

function ruleNewIds(rule: MappingRule): string[] {
  return rule.kind === 'split' ? rule.newIds : [rule.newId];
}

export interface RuleValidationResult {
  accepted: MappingRule[];
  rejections: RuleRejection[];
}

/**
 * 校验规则集合：引用的语句必须存在、identity 要求指纹一致、
 * 同一语句不得被多条规则占用。校验失败的规则被拒绝，
 * 涉及的语句回落到自动匹配 / 不确定项，绝不强行继承。
 */
export function validateRules(
  rules: MappingRule[],
  oldStatements: ReadonlyMap<string, OldStatement>,
  newStatements: ReadonlyMap<string, NewStatement>,
): RuleValidationResult {
  const accepted: MappingRule[] = [];
  const rejections: RuleRejection[] = [];
  const usedOld = new Set<string>();
  const usedNew = new Set<string>();

  for (const rule of rules) {
    const oldIds = ruleOldIds(rule);
    const newIds = ruleNewIds(rule);

    const missingOld = oldIds.filter((id) => !oldStatements.has(id));
    if (missingOld.length > 0) {
      rejections.push({ rule, reason: `旧语句不存在: ${missingOld.join(', ')}` });
      continue;
    }
    const missingNew = newIds.filter((id) => !newStatements.has(id));
    if (missingNew.length > 0) {
      rejections.push({ rule, reason: `新语句不存在: ${missingNew.join(', ')}` });
      continue;
    }
    if (rule.kind === 'split' && rule.newIds.length < 2) {
      rejections.push({ rule, reason: 'split 规则至少需要两个目标语句' });
      continue;
    }
    if (rule.kind === 'merge' && rule.oldIds.length < 2) {
      rejections.push({ rule, reason: 'merge 规则至少需要两个来源语句' });
      continue;
    }
    if (rule.kind === 'identity') {
      const oldFp = oldStatements.get(rule.oldId)!.fingerprint;
      const newFp = newStatements.get(rule.newId)!.fingerprint;
      if (oldFp !== newFp) {
        rejections.push({
          rule,
          reason: `identity 规则要求内容指纹一致（${rule.oldId}=${oldFp}，${rule.newId}=${newFp}）`,
        });
        continue;
      }
    }
    if ((rule.kind === 'rename' || rule.kind === 'split' || rule.kind === 'merge') && !rule.reason.trim()) {
      rejections.push({ rule, reason: '该规则必须填写人工确认理由 reason' });
      continue;
    }
    const conflictOld = oldIds.filter((id) => usedOld.has(id));
    const conflictNew = newIds.filter((id) => usedNew.has(id));
    if (conflictOld.length > 0 || conflictNew.length > 0) {
      rejections.push({
        rule,
        reason: `语句已被其他规则占用: ${[...conflictOld, ...conflictNew].join(', ')}`,
      });
      continue;
    }

    for (const id of oldIds) usedOld.add(id);
    for (const id of newIds) usedNew.add(id);
    accepted.push(rule);
  }

  return { accepted, rejections };
}

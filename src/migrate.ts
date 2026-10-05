import type { NewStatement, OldStatement, SourceRange } from './types.ts';
import { validateRules, type MappingRule, type RuleRejection } from './rules.ts';

/** 一条覆盖继承的来源记录。多对一合并时来源分别保留，不做糅合。 */
export interface Provenance {
  oldId: string;
  hits: number;
}

export type InheritVia = 'auto-identical' | 'identity-rule' | 'rename-rule' | 'split' | 'merge';

/** 继承项：新语句 + 已证明的来源。 */
export interface InheritedEntry {
  newId: string;
  via: InheritVia;
  /**
   * 继承得到的覆盖次数。
   * 一对一 / 拆分：等于来源 hits；合并：取各来源最小值（保守估计）。
   */
  hits: number;
  sources: Provenance[];
  note?: string;
}

/** 新增项：新版本中无法证明来源的语句，覆盖从零开始。 */
export interface AddedEntry {
  newId: string;
  range: SourceRange;
}

/**
 * 不确定项：存在疑点但无法证明对应关系，需要人工补充映射规则。
 * 这些语句不会继承任何覆盖数据。
 */
export type UncertainEntry =
  | {
      type: 'ambiguous-fingerprint';
      fingerprint: string;
      oldIds: string[];
      newIds: string[];
      detail: string;
    }
  | {
      type: 'rejected-rule';
      rule: MappingRule;
      reason: string;
      affectedOldIds: string[];
      affectedNewIds: string[];
    }
  | {
      type: 'unmatched-old';
      oldId: string;
      range: SourceRange;
      detail: string;
    };

/** 迁移后的新基线覆盖记录。 */
export interface MigratedStatement {
  id: string;
  range: SourceRange;
  hits: number;
  fingerprint: string;
  provenance: Provenance[];
}

export interface MigrationSummary {
  oldTotal: number;
  newTotal: number;
  inherited: number;
  added: number;
  uncertain: number;
  rejectedRules: number;
}

export interface MigrationReport {
  summary: MigrationSummary;
  inherited: InheritedEntry[];
  added: AddedEntry[];
  uncertain: UncertainEntry[];
  rejectedRules: RuleRejection[];
  migratedCoverage: MigratedStatement[];
}

function groupByFingerprint<T extends { id: string; fingerprint: string }>(
  statements: Iterable<T>,
): Map<string, T[]> {
  const groups = new Map<string, T[]>();
  for (const stmt of statements) {
    const group = groups.get(stmt.fingerprint);
    if (group) {
      group.push(stmt);
    } else {
      groups.set(stmt.fingerprint, [stmt]);
    }
  }
  return groups;
}

/**
 * 覆盖率迁移主流程。
 *
 * 继承依据（仅此两类，均与行号无关）：
 * 1. 显式映射规则（通过 validateRules 校验）；
 * 2. 内容指纹唯一匹配（同一指纹在两侧各只出现一次，可证明是同一语句的移动）。
 *
 * 指纹一对多 / 多对多的情况不做猜测，全部进入不确定项。
 */
export function migrateCoverage(
  oldStatements: OldStatement[],
  newStatements: NewStatement[],
  rules: MappingRule[] = [],
): MigrationReport {
  const oldById = new Map(oldStatements.map((s) => [s.id, s]));
  const newById = new Map(newStatements.map((s) => [s.id, s]));

  const inherited: InheritedEntry[] = [];
  const uncertain: UncertainEntry[] = [];
  const matchedOld = new Set<string>();
  const matchedNew = new Set<string>();

  // 1. 显式规则优先。
  const { accepted, rejections } = validateRules(rules, oldById, newById);
  for (const { rule, reason } of rejections) {
    uncertain.push({
      type: 'rejected-rule',
      rule,
      reason,
      affectedOldIds: rule.kind === 'merge' ? rule.oldIds : [rule.oldId],
      affectedNewIds: rule.kind === 'split' ? rule.newIds : [rule.newId],
    });
  }

  for (const rule of accepted) {
    if (rule.kind === 'identity' || rule.kind === 'rename') {
      const source = oldById.get(rule.oldId)!;
      inherited.push({
        newId: rule.newId,
        via: rule.kind === 'identity' ? 'identity-rule' : 'rename-rule',
        hits: source.hits,
        sources: [{ oldId: source.id, hits: source.hits }],
      });
      matchedOld.add(rule.oldId);
      matchedNew.add(rule.newId);
    } else if (rule.kind === 'split') {
      const source = oldById.get(rule.oldId)!;
      for (const newId of rule.newIds) {
        inherited.push({
          newId,
          via: 'split',
          hits: source.hits,
          sources: [{ oldId: source.id, hits: source.hits }],
          note: `拆分自 ${source.id}，各分支共享来源覆盖次数，未经逐分支验证`,
        });
        matchedNew.add(newId);
      }
      matchedOld.add(rule.oldId);
    } else {
      const sources = rule.oldIds.map((oldId) => {
        const source = oldById.get(oldId)!;
        return { oldId, hits: source.hits };
      });
      inherited.push({
        newId: rule.newId,
        via: 'merge',
        hits: Math.min(...sources.map((s) => s.hits)),
        sources,
        note: '合并语句的 hits 取各来源最小值（保守），来源分别保留',
      });
      for (const oldId of rule.oldIds) matchedOld.add(oldId);
      matchedNew.add(rule.newId);
    }
  }

  // 2. 内容指纹唯一匹配（覆盖“纯移动”场景，与行号无关）。
  const remainingOld = oldStatements.filter((s) => !matchedOld.has(s.id));
  const remainingNew = newStatements.filter((s) => !matchedNew.has(s.id));
  const oldByFp = groupByFingerprint(remainingOld);
  const newByFp = groupByFingerprint(remainingNew);

  for (const [fingerprint, oldGroup] of oldByFp) {
    const newGroup = newByFp.get(fingerprint);
    if (!newGroup) continue;
    if (oldGroup.length === 1 && newGroup.length === 1) {
      const source = oldGroup[0];
      const target = newGroup[0];
      inherited.push({
        newId: target.id,
        via: 'auto-identical',
        hits: source.hits,
        sources: [{ oldId: source.id, hits: source.hits }],
      });
      matchedOld.add(source.id);
      matchedNew.add(target.id);
    } else {
      uncertain.push({
        type: 'ambiguous-fingerprint',
        fingerprint,
        oldIds: oldGroup.map((s) => s.id),
        newIds: newGroup.map((s) => s.id),
        detail: '相同内容在两侧出现多次，无法唯一确定对应关系，需要显式规则',
      });
      for (const s of oldGroup) matchedOld.add(s.id);
      for (const s of newGroup) matchedNew.add(s.id);
    }
  }

  // 3. 剩余语句分类：新语句为新增，旧语句为不确定（可能删除或已变更）。
  const added: AddedEntry[] = [];
  for (const stmt of newStatements) {
    if (!matchedNew.has(stmt.id)) {
      added.push({ newId: stmt.id, range: stmt.range });
    }
  }
  for (const stmt of oldStatements) {
    if (!matchedOld.has(stmt.id)) {
      uncertain.push({
        type: 'unmatched-old',
        oldId: stmt.id,
        range: stmt.range,
        detail: '旧语句在新版本中无对应项，可能已删除或内容已变更；如需继承请提供显式规则',
      });
    }
  }

  // 4. 生成新基线：继承项带来源，新增项从零开始。
  const inheritedByNewId = new Map(inherited.map((entry) => [entry.newId, entry]));
  const migratedCoverage: MigratedStatement[] = newStatements.map((stmt) => {
    const entry = inheritedByNewId.get(stmt.id);
    return {
      id: stmt.id,
      range: stmt.range,
      hits: entry ? entry.hits : 0,
      fingerprint: stmt.fingerprint,
      provenance: entry ? entry.sources : [],
    };
  });

  return {
    summary: {
      oldTotal: oldStatements.length,
      newTotal: newStatements.length,
      inherited: inherited.length,
      added: added.length,
      uncertain: uncertain.length,
      rejectedRules: rejections.length,
    },
    inherited,
    added,
    uncertain,
    rejectedRules: rejections,
    migratedCoverage,
  };
}

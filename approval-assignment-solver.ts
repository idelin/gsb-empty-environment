export type Role = 'initiate' | 'review' | 'pay';

export const ROLES: Role[] = ['initiate', 'review', 'pay'];

export const ROLE_LABEL: Record<Role, string> = {
  initiate: '发起',
  review: '复核',
  pay: '付款',
};

export type AvoidanceRule =
  | { kind: 'role-ban'; id: string; person: string; role: Role; reason: string }
  | {
      kind: 'pair-conflict';
      id: string;
      personA: string;
      roleA: Role;
      personB: string;
      roleB: Role;
      reason: string;
    };

export interface SolverInput {
  candidates: Record<Role, string[]>;
  proxies: Array<[string, string]>;
  avoidances: AvoidanceRule[];
}

export interface Exclusion {
  role: Role;
  candidate: string;
  reason: string;
}

export interface Step {
  index: number;
  title: string;
  notes: string[];
  exclusions: Exclusion[];
}

export interface SolveResult {
  feasible: boolean;
  assignment: Partial<Record<Role, string>>;
  steps: Step[];
  stepLimitHit: boolean;
  minimalRelaxations: AvoidanceRule[][];
  relaxedAssignment: Partial<Record<Role, string>> | null;
  structuralReason: string | null;
}

export const MAX_STEPS = 15;
const MAX_RELAXATION_ENUM = 4096;

type PairConflictRule = Extract<AvoidanceRule, { kind: 'pair-conflict' }>;
type RoleBanRule = Extract<AvoidanceRule, { kind: 'role-ban' }>;

class UnionFind {
  private parent = new Map<string, string>();

  find(x: string): string {
    let root = this.parent.get(x);
    if (root === undefined) {
      this.parent.set(x, x);
      return x;
    }
    if (root !== x) {
      root = this.find(root);
      this.parent.set(x, root);
    }
    return root;
  }

  union(a: string, b: string): void {
    const ra = this.find(a);
    const rb = this.find(b);
    if (ra !== rb) this.parent.set(ra, rb);
  }
}

function* combinations<T>(items: T[], k: number): Generator<T[]> {
  if (k === 0) {
    yield [];
    return;
  }
  for (let i = 0; i <= items.length - k; i++) {
    for (const rest of combinations(items.slice(i + 1), k - 1)) {
      yield [items[i], ...rest];
    }
  }
}

interface DomainEntry {
  person: string;
  entity: string;
}

export function solveApprovalAssignment(input: SolverInput): SolveResult {
  const steps: Step[] = [];
  let stepLimitHit = false;

  const uf = new UnionFind();
  const people = new Set<string>();
  for (const role of ROLES) for (const p of input.candidates[role] ?? []) people.add(p);
  for (const [a, b] of input.proxies) {
    people.add(a);
    people.add(b);
  }
  for (const rule of input.avoidances) {
    if (rule.kind === 'role-ban') people.add(rule.person);
    else {
      people.add(rule.personA);
      people.add(rule.personB);
    }
  }
  for (const p of people) uf.find(p);
  for (const [a, b] of input.proxies) uf.union(a, b);
  const entityOf = (p: string): string => uf.find(p);

  const addStep = (title: string): Step | null => {
    if (steps.length >= MAX_STEPS) {
      stepLimitHit = true;
      return null;
    }
    const step: Step = { index: steps.length + 1, title, notes: [], exclusions: [] };
    steps.push(step);
    return step;
  };

  const s1 = addStep('构建代理等价类（代理关系归并为同一责任主体）');
  const groups = new Map<string, string[]>();
  for (const p of people) {
    const root = uf.find(p);
    const g = groups.get(root) ?? [];
    g.push(p);
    groups.set(root, g);
  }
  for (const g of groups.values()) {
    if (g.length > 1) s1?.notes.push(`${g.join('、')} 互为代理，视为同一责任主体`);
  }
  if (s1 && s1.notes.length === 0) s1.notes.push('无代理关系，各候选人均为独立责任主体');

  const buildDomains = (
    activeRules: AvoidanceRule[],
    step: Step | null,
  ): Record<Role, DomainEntry[]> => {
    const bans = activeRules.filter((r): r is RoleBanRule => r.kind === 'role-ban');
    const domains = {} as Record<Role, DomainEntry[]>;
    for (const role of ROLES) {
      const seen = new Set<string>();
      const entries: DomainEntry[] = [];
      for (const person of input.candidates[role] ?? []) {
        const entity = entityOf(person);
        const ban = bans.find((b) => b.role === role && entityOf(b.person) === entity);
        if (ban) {
          step?.exclusions.push({
            role,
            candidate: person,
            reason: `回避规则 ${ban.id}（岗位禁任）：${ban.reason}`,
          });
          continue;
        }
        if (seen.has(entity)) {
          step?.notes.push(
            `${person} 与已保留候选属同一责任主体（本人或代理），归并处理，不重复列出`,
          );
          continue;
        }
        seen.add(entity);
        entries.push({ person, entity });
      }
      domains[role] = entries;
    }
    return domains;
  };

  const search = (
    activeRules: AvoidanceRule[],
    record: boolean,
  ): Partial<Record<Role, string>> | null => {
    let domains: Record<Role, DomainEntry[]>;
    if (record) {
      const s2 = addStep('应用回避规则（岗位禁任）过滤候选人');
      domains = buildDomains(activeRules, s2);
      if (s2 && s2.exclusions.length === 0 && s2.notes.length === 0) {
        s2.notes.push('无岗位禁任规则命中，候选人不变');
      }
    } else {
      domains = buildDomains(activeRules, null);
    }

    const pairRules = activeRules.filter(
      (r): r is PairConflictRule => r.kind === 'pair-conflict',
    );
    const assignment: Partial<Record<Role, string>> = {};
    const assignedEntity = new Map<Role, string>();

    const pairHit = (role: Role, entity: string): PairConflictRule | undefined =>
      pairRules.find((r) => {
        for (const [otherRole, otherEntity] of assignedEntity) {
          const forward =
            r.roleA === role &&
            r.roleB === otherRole &&
            entityOf(r.personA) === entity &&
            entityOf(r.personB) === otherEntity;
          const backward =
            r.roleB === role &&
            r.roleA === otherRole &&
            entityOf(r.personB) === entity &&
            entityOf(r.personA) === otherEntity;
          if (forward || backward) return true;
        }
        return false;
      });

    const bt = (idx: number): boolean => {
      if (record && stepLimitHit) return false;
      if (idx === ROLES.length) return true;
      const role = ROLES[idx];
      const step = record ? addStep(`为「${ROLE_LABEL[role]}」选择责任人`) : null;
      if (record && !step) return false;
      for (const cand of domains[role]) {
        const conflictRole = ROLES.slice(0, idx).find(
          (r) => assignedEntity.get(r) === cand.entity,
        );
        if (conflictRole) {
          const other = assignment[conflictRole]!;
          step?.exclusions.push({
            role,
            candidate: cand.person,
            reason:
              cand.person === other
                ? `与「${ROLE_LABEL[conflictRole]}」的 ${other} 为同一人，违反职责分离`
                : `与「${ROLE_LABEL[conflictRole]}」的 ${other} 互为代理，属同一责任主体，违反职责分离`,
          });
          continue;
        }
        const hit = pairHit(role, cand.entity);
        if (hit) {
          step?.exclusions.push({
            role,
            candidate: cand.person,
            reason: `触发回避规则 ${hit.id}（${hit.personA}↔${hit.personB}）：${hit.reason}`,
          });
          continue;
        }
        assignment[role] = cand.person;
        assignedEntity.set(role, cand.entity);
        step?.notes.push(`暂定 ${cand.person}`);
        if (bt(idx + 1)) return true;
        delete assignment[role];
        assignedEntity.delete(role);
        step?.notes.push(`后续角色无可行指派，撤回 ${cand.person}`);
      }
      if (step) {
        if (domains[role].length === 0) step.notes.push('候选为空');
        step.notes.push(`「${ROLE_LABEL[role]}」无法确定人选，回溯`);
      }
      return false;
    };

    return bt(0) ? assignment : null;
  };

  const assignment = search(input.avoidances, true);
  if (assignment) {
    return {
      feasible: true,
      assignment,
      steps,
      stepLimitHit,
      minimalRelaxations: [],
      relaxedAssignment: null,
      structuralReason: null,
    };
  }

  const minimalRelaxations: AvoidanceRule[][] = [];
  let relaxedAssignment: Partial<Record<Role, string>> | null = null;
  let structuralReason: string | null = null;

  if (!stepLimitHit) {
    const rules = input.avoidances;
    let budget = MAX_RELAXATION_ENUM;
    for (let k = 1; k <= rules.length; k++) {
      for (const subset of combinations(rules, k)) {
        if (--budget < 0) break;
        const remaining = rules.filter((r) => !subset.includes(r));
        const a = search(remaining, false);
        if (a) {
          minimalRelaxations.push(subset);
          if (!relaxedAssignment) relaxedAssignment = a;
        }
      }
      if (minimalRelaxations.length > 0 || budget < 0) break;
    }
    if (minimalRelaxations.length === 0 && budget >= 0) {
      const emptyRole = ROLES.find((r) => (input.candidates[r] ?? []).length === 0);
      const entityCount = new Set(
        [...people]
          .filter((p) => ROLES.some((r) => (input.candidates[r] ?? []).includes(p)))
          .map(entityOf),
      ).size;
      if (emptyRole) {
        structuralReason = `「${ROLE_LABEL[emptyRole]}」无任何候选人`;
      } else if (entityCount < ROLES.length) {
        structuralReason = `候选仅覆盖 ${entityCount} 个责任主体，少于 ${ROLES.length} 个角色，职责分离（含代理等价）无法满足`;
      } else {
        structuralReason = '即使移除全部回避规则仍不可行';
      }
    }
  }

  const sEnd = addStep('问题不可行，分析最小冲突约束');
  if (sEnd) {
    if (structuralReason) {
      sEnd.notes.push(`结构性不可行：${structuralReason}`);
    } else if (minimalRelaxations.length > 0) {
      for (const relax of minimalRelaxations) {
        sEnd.notes.push(
          `最小冲突约束集：${relax.map((r) => r.id).join('、')}（放宽该集合即可恢复可行）`,
        );
      }
    } else {
      sEnd.notes.push('无法在枚举预算内确定最小冲突约束集');
    }
  }

  return {
    feasible: false,
    assignment: {},
    steps,
    stepLimitHit,
    minimalRelaxations,
    relaxedAssignment,
    structuralReason,
  };
}

export function describeRule(rule: AvoidanceRule): string {
  return rule.kind === 'role-ban'
    ? `${rule.person} 不得担任「${ROLE_LABEL[rule.role]}」：${rule.reason}`
    : `${rule.personA}（${ROLE_LABEL[rule.roleA]}）与 ${rule.personB}（${ROLE_LABEL[rule.roleB]}）冲突：${rule.reason}`;
}

export function formatResult(result: SolveResult): string {
  const lines: string[] = [];
  for (const step of result.steps) {
    lines.push(`步骤 ${step.index}：${step.title}`);
    for (const e of step.exclusions) {
      lines.push(`  ✗ 排除「${ROLE_LABEL[e.role]}」候选 ${e.candidate}：${e.reason}`);
    }
    for (const n of step.notes) lines.push(`  · ${n}`);
  }
  if (result.stepLimitHit) lines.push(`（步骤达到上限 ${MAX_STEPS}，搜索终止）`);
  if (result.feasible) {
    lines.push('可行指派：');
    for (const role of ROLES) lines.push(`  ${ROLE_LABEL[role]}：${result.assignment[role]}`);
  } else if (result.structuralReason) {
    lines.push(`不可行（结构性）：${result.structuralReason}`);
  } else if (result.minimalRelaxations.length > 0) {
    lines.push('不可行。最小冲突约束（放宽任一集合即可恢复可行）：');
    for (const relax of result.minimalRelaxations) {
      lines.push(`  - ${relax.map((r) => `${r.id}（${describeRule(r)}）`).join('、')}`);
    }
    if (result.relaxedAssignment) {
      lines.push('放宽首个冲突集后的可行指派：');
      for (const role of ROLES) {
        lines.push(`  ${ROLE_LABEL[role]}：${result.relaxedAssignment[role]}`);
      }
    }
  } else {
    lines.push('不可行，且未能确定最小冲突约束。');
  }
  return lines.join('\n');
}

function demo(): void {
  const scenarioA: SolverInput = {
    candidates: {
      initiate: ['张三', '李四'],
      review: ['王五', '陈九'],
      pay: ['赵六', '王五'],
    },
    proxies: [['陈九', '张三']],
    avoidances: [
      { kind: 'role-ban', id: 'R1', person: '赵六', role: 'pay', reason: '赵六已申报与供应商存在利益关系，不得经手付款' },
      { kind: 'pair-conflict', id: 'R2', personA: '王五', roleA: 'review', personB: '赵六', roleB: 'pay', reason: '王五与赵六为直系亲属，须回避' },
    ],
  };

  const scenarioB: SolverInput = {
    candidates: {
      initiate: ['张三'],
      review: ['李四', '王五'],
      pay: ['李四', '王五'],
    },
    proxies: [],
    avoidances: [
      { kind: 'pair-conflict', id: 'R1', personA: '李四', roleA: 'review', personB: '王五', roleB: 'pay', reason: '二人系夫妻，须回避' },
      { kind: 'pair-conflict', id: 'R2', personA: '王五', roleA: 'review', personB: '李四', roleB: 'pay', reason: '二人系夫妻，须回避' },
    ],
  };

  const scenarioC: SolverInput = {
    candidates: {
      initiate: ['张三'],
      review: ['陈九'],
      pay: ['孙七'],
    },
    proxies: [['张三', '陈九']],
    avoidances: [],
  };

  console.log('=== 场景 A：存在可行指派 ===');
  console.log(formatResult(solveApprovalAssignment(scenarioA)));
  console.log('');
  console.log('=== 场景 B：不可行，输出最小冲突约束 ===');
  console.log(formatResult(solveApprovalAssignment(scenarioB)));
  console.log('');
  console.log('=== 场景 C：结构性不可行（责任主体不足） ===');
  console.log(formatResult(solveApprovalAssignment(scenarioC)));
}

declare const require: { main: unknown } | undefined;
declare const module: unknown;
if (typeof require !== 'undefined' && require.main === module) demo();

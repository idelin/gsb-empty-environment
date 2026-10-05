// 审批指派求解器
// 业务规则：发起、复核、付款三个角色必须由不同的责任主体承担；
// 代理关系（delegation）将多个人合并为同一责任主体，回避规则同样按责任主体解释（代理视同本人）。
// 输入：各角色候选人、代理等价关系、回避规则；
// 输出：可行指派；若不可行，输出最小冲突约束集（极小不可行子集）。
// 求解步数上限默认 15 步，并逐步记录每个候选被排除的具体原因。

type Role = 'initiate' | 'review' | 'pay';
type SolveStatus = 'feasible' | 'infeasible' | 'step-limit';

const ROLE_ORDER: Role[] = ['initiate', 'review', 'pay'];
const ROLE_LABEL: Record<Role, string> = { initiate: '发起', review: '复核', pay: '付款' };
const DEFAULT_MAX_STEPS = 15;

interface PersonRoleRule {
  kind: 'person-role';
  person: string;
  role: Role;
  note: string;
}

interface PairRule {
  kind: 'pair';
  a: string;
  b: string;
  note: string;
}

type AvoidRule = PersonRoleRule | PairRule;

interface Delegation {
  a: string;
  b: string;
}

interface SolverInput {
  candidates: Record<Role, string[]>;
  delegations: Delegation[];
  avoidances: AvoidRule[];
}

interface StepLog {
  step: number;
  role: Role;
  events: string[];
}

interface SolveResult {
  status: SolveStatus;
  assignment: Partial<Record<Role, string>>;
  steps: StepLog[];
}

interface SubjectMap {
  subjectOf: (person: string) => string;
  labelOf: (subject: string) => string;
}

interface Constraint {
  id: string;
  describe: string;
}

function collectPersons(input: SolverInput): string[] {
  const seen: Set<string> = new Set();
  for (const role of ROLE_ORDER) {
    const list = input.candidates[role] || [];
    for (const p of list) seen.add(p);
  }
  for (const d of input.delegations) {
    seen.add(d.a);
    seen.add(d.b);
  }
  for (const rule of input.avoidances) {
    if (rule.kind === 'person-role') seen.add(rule.person);
    if (rule.kind === 'pair') {
      seen.add(rule.a);
      seen.add(rule.b);
    }
  }
  return Array.from(seen);
}

// 并查集：把互为代理的人合并为同一责任主体
function buildSubjects(persons: string[], delegations: Delegation[]): SubjectMap {
  const parent: Map<string, string> = new Map();
  for (const p of persons) parent.set(p, p);

  function subjectOf(x: string): string {
    if (!parent.has(x)) parent.set(x, x);
    let root = parent.get(x) || x;
    if (root !== x) {
      root = subjectOf(root);
      parent.set(x, root);
    }
    return root;
  }

  for (const d of delegations) {
    const ra = subjectOf(d.a);
    const rb = subjectOf(d.b);
    if (ra !== rb) parent.set(ra, rb);
  }

  const members: Map<string, string[]> = new Map();
  for (const p of persons) {
    const root = subjectOf(p);
    if (!members.has(root)) members.set(root, []);
    (members.get(root) || []).push(p);
  }
  const labels: Map<string, string> = new Map();
  for (const entry of members) {
    const list = entry[1].slice().sort();
    labels.set(entry[0], list.join('＝'));
  }

  function labelOf(subject: string): string {
    return labels.get(subject) || subject;
  }

  return { subjectOf, labelOf };
}

// 候选 person 在已部分指派 assigned 下被排除的全部具体原因
function exclusionReasons(
  role: Role,
  person: string,
  assigned: Partial<Record<Role, string>>,
  subjects: SubjectMap,
  avoidances: AvoidRule[],
): string[] {
  const reasons: string[] = [];
  const subj = subjects.subjectOf(person);

  for (const r of ROLE_ORDER) {
    const holder = assigned[r];
    if (holder !== undefined && subjects.subjectOf(holder) === subj) {
      reasons.push(
        '与已指派的' + ROLE_LABEL[r] + '（' + holder + '）同属责任主体「' + subjects.labelOf(subj) + '」（代理等价，职责未分离）',
      );
    }
  }

  for (const rule of avoidances) {
    if (rule.kind === 'person-role' && rule.role === role && subjects.subjectOf(rule.person) === subj) {
      let msg = '回避规则：' + rule.person + ' 不得担任' + ROLE_LABEL[role];
      if (rule.person !== person) msg += '（' + person + ' 与其同属一个责任主体，视同本人）';
      if (rule.note !== '') msg += '（' + rule.note + '）';
      reasons.push(msg);
    }
    if (rule.kind === 'pair') {
      for (const r of ROLE_ORDER) {
        const other = assigned[r];
        if (other === undefined) continue;
        const otherSubj = subjects.subjectOf(other);
        const hit =
          (subjects.subjectOf(rule.a) === subj && subjects.subjectOf(rule.b) === otherSubj) ||
          (subjects.subjectOf(rule.b) === subj && subjects.subjectOf(rule.a) === otherSubj);
        if (hit) {
          let msg = '回避规则：' + person + ' 与 ' + other + '（已任' + ROLE_LABEL[r] + '）不得同时参与审批链';
          if (rule.note !== '') msg += '（' + rule.note + '）';
          reasons.push(msg);
        }
      }
    }
  }
  return reasons;
}

// 回溯搜索：每个决策节点计为一步，步数超过 maxSteps 即中止
function solve(input: SolverInput, maxSteps: number, withLog: boolean): SolveResult {
  const subjects = buildSubjects(collectPersons(input), input.delegations);
  const assigned: Partial<Record<Role, string>> = {};
  const steps: StepLog[] = [];
  let stepCount = 0;
  let hitLimit = false;

  function dfs(idx: number): boolean {
    if (idx >= ROLE_ORDER.length) return true;
    if (stepCount >= maxSteps) {
      hitLimit = true;
      return false;
    }
    const role = ROLE_ORDER[idx];
    stepCount += 1;
    const step: StepLog = { step: stepCount, role, events: [] };
    if (withLog) steps.push(step);

    const cands = input.candidates[role] || [];
    let anyViable = false;
    for (const cand of cands) {
      const reasons = exclusionReasons(role, cand, assigned, subjects, input.avoidances);
      if (reasons.length > 0) {
        if (withLog) {
          for (const reason of reasons) step.events.push('✗ 排除 ' + cand + '：' + reason);
        }
        continue;
      }
      anyViable = true;
      if (withLog) step.events.push('→ 试派 ' + cand);
      assigned[role] = cand;
      if (dfs(idx + 1)) {
        if (withLog) step.events.push('✓ 确定 ' + cand);
        return true;
      }
      delete assigned[role];
      if (hitLimit) return false;
      if (withLog) step.events.push('↩ 回溯：' + cand + ' 担任「' + ROLE_LABEL[role] + '」后，后续角色无可行人选');
    }
    if (withLog && !anyViable) step.events.push('⚠ 候选全部被排除，此路不通');
    if (withLog && anyViable) step.events.push('⚠ 所有可行候选均导致后续失败');
    return false;
  }

  const ok = dfs(0);
  let status: SolveStatus = 'infeasible';
  if (ok) status = 'feasible';
  if (!ok && hitLimit) status = 'step-limit';
  return { status, assignment: assigned, steps };
}

function isFeasible(input: SolverInput): boolean {
  return solve(input, 1000000, false).status === 'feasible';
}

// 把输入约束展开为可逐条引用的约束清单
function listConstraints(input: SolverInput): Constraint[] {
  const out: Constraint[] = [];
  input.delegations.forEach((d, i) => {
    out.push({ id: 'delegation-' + i, describe: '代理等价：' + d.a + ' 与 ' + d.b + ' 属同一责任主体' });
  });
  input.avoidances.forEach((rule, i) => {
    if (rule.kind === 'person-role') {
      out.push({ id: 'avoidance-' + i, describe: '回避规则：' + rule.person + ' 不得担任' + ROLE_LABEL[rule.role] });
    } else {
      out.push({ id: 'avoidance-' + i, describe: '回避规则：' + rule.a + ' 与 ' + rule.b + ' 不得同时参与审批链' });
    }
  });
  return out;
}

function withConstraints(input: SolverInput, keep: Constraint[]): SolverInput {
  const keepIds: Set<string> = new Set();
  for (const c of keep) keepIds.add(c.id);
  const delegations: Delegation[] = [];
  input.delegations.forEach((d, i) => {
    if (keepIds.has('delegation-' + i)) delegations.push(d);
  });
  const avoidances: AvoidRule[] = [];
  input.avoidances.forEach((rule, i) => {
    if (keepIds.has('avoidance-' + i)) avoidances.push(rule);
  });
  return { candidates: input.candidates, delegations, avoidances };
}

// 删除法求极小不可行子集：逐条试删，删除后仍不可行则永久移除
function minimizeConflict(input: SolverInput): Constraint[] {
  let core = listConstraints(input);
  if (isFeasible(withConstraints(input, core))) return [];
  for (const c of core.slice()) {
    const trial: Constraint[] = [];
    for (const x of core) {
      if (x.id !== c.id) trial.push(x);
    }
    if (!isFeasible(withConstraints(input, trial))) core = trial;
  }
  return core;
}

function structuralConflicts(input: SolverInput): string[] {
  const out: string[] = [];
  for (const role of ROLE_ORDER) {
    const list = input.candidates[role] || [];
    if (list.length === 0) out.push('角色「' + ROLE_LABEL[role] + '」没有任何候选人');
  }
  return out;
}

function formatSteps(steps: StepLog[]): string {
  const lines: string[] = [];
  for (const step of steps) {
    lines.push('步骤 ' + step.step + '｜为「' + ROLE_LABEL[step.role] + '」选人');
    for (const ev of step.events) lines.push('  ' + ev);
  }
  return lines.join('\n');
}

function report(input: SolverInput): string {
  const lines: string[] = [];
  const result = solve(input, DEFAULT_MAX_STEPS, true);
  lines.push('—— 求解过程（步数上限 ' + DEFAULT_MAX_STEPS + '）——');
  lines.push(formatSteps(result.steps));

  if (result.status === 'feasible') {
    lines.push('结论：找到可行指派');
    for (const role of ROLE_ORDER) {
      lines.push('  ' + ROLE_LABEL[role] + '：' + (result.assignment[role] || '？'));
    }
    return lines.join('\n');
  }

  if (result.status === 'step-limit') {
    lines.push('结论：超过 ' + DEFAULT_MAX_STEPS + ' 步仍未找到可行指派');
  } else {
    lines.push('结论：不存在可行指派');
  }

  const structural = structuralConflicts(input);
  if (structural.length > 0) {
    lines.push('结构性冲突（与约束无关）：');
    for (const s of structural) lines.push('  - ' + s);
  }

  const core = minimizeConflict(input);
  if (core.length > 0) {
    lines.push('最小冲突约束集（其中任意一条被移除即可恢复可行）：');
    for (const c of core) lines.push('  - ' + c.describe);
  }
  return lines.join('\n');
}

function demo(): void {
  const inputA: SolverInput = {
    candidates: {
      initiate: ['张伟'],
      review: ['李娜', '赵敏'],
      pay: ['王强', '陈芳'],
    },
    delegations: [{ a: '张伟', b: '李娜' }],
    avoidances: [{ kind: 'pair', a: '赵敏', b: '王强', note: '亲属关系回避' }],
  };
  console.log('=== 场景一：存在可行指派 ===');
  console.log(report(inputA));

  const inputB: SolverInput = {
    candidates: {
      initiate: ['张伟', '李娜'],
      review: ['王强'],
      pay: ['赵敏'],
    },
    delegations: [
      { a: '张伟', b: '王强' },
      { a: '李娜', b: '王强' },
    ],
    avoidances: [{ kind: 'person-role', person: '陈芳', role: 'pay', note: '利益冲突申报' }],
  };
  console.log('');
  console.log('=== 场景二：不可行，输出最小冲突约束 ===');
  console.log(report(inputB));
}

demo();

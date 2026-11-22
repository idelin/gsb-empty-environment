/**
 * 证明选择库：为 TypeScript 决策宿主在最小披露前提下选择一组足以证明授权结论的
 * 签名声明 / 集合成员证明，并产出可独立验证的充分性依据。
 *
 * 核心概念：
 * - 策略（Policy）：有序的许可/禁止规则，最后一条匹配的规则生效，无匹配时取默认效果。
 * - 条件（Condition）：等值、有限集合成员、否定、AND、OR。
 * - 属性域（Domain）：每个属性的有限取值域，以及不可同时成立的组合（部分赋值）。
 * - 证明（Proof）：确值声明（claim）或集合成员证明（setMembership，不揭示确值）。
 * - 未披露 = 未知：未被证明约束的属性可取其域内任意合法值。
 */

// ---------------------------------------------------------------------------
// 基础类型
// ---------------------------------------------------------------------------

export type AttrValue = string | number | boolean;
export type Assignment = Readonly<Record<string, AttrValue>>;

export type Condition =
  | { op: 'eq'; attr: string; value: AttrValue }
  | { op: 'in'; attr: string; values: readonly AttrValue[] }
  | { op: 'not'; cond: Condition }
  | { op: 'and'; conds: readonly Condition[] }
  | { op: 'or'; conds: readonly Condition[] };

export type Effect = 'permit' | 'deny';

export interface Rule {
  effect: Effect;
  when: Condition;
}

export interface Policy {
  rules: readonly Rule[];
  /** 无任何规则匹配时的默认效果，缺省为 'deny'。 */
  defaultEffect?: Effect;
}

export interface Domain {
  /** 每个属性的有限取值域。 */
  attributes: Readonly<Record<string, readonly AttrValue[]>>;
  /** 不可同时成立的组合：每个元素是一个部分赋值，任何完整赋值若匹配其全部键值对即为非法。 */
  incompatible?: readonly Assignment[];
}

// ---------------------------------------------------------------------------
// 条件与策略求值
// ---------------------------------------------------------------------------

export function evalCondition(cond: Condition, assignment: Assignment): boolean {
  switch (cond.op) {
    case 'eq':
      return assignment[cond.attr] === cond.value;
    case 'in':
      return cond.values.includes(assignment[cond.attr]);
    case 'not':
      return !evalCondition(cond.cond, assignment);
    case 'and':
      return cond.conds.every((c) => evalCondition(c, assignment));
    case 'or':
      return cond.conds.some((c) => evalCondition(c, assignment));
  }
}

/** 最后一条匹配的规则生效；无匹配时取默认效果（默认 deny）。 */
export function decide(policy: Policy, assignment: Assignment): Effect {
  let effect: Effect = policy.defaultEffect ?? 'deny';
  for (const rule of policy.rules) {
    if (evalCondition(rule.when, assignment)) {
      effect = rule.effect;
    }
  }
  return effect;
}

/** 收集条件中引用到的全部属性名。 */
export function conditionAttributes(cond: Condition, into: Set<string> = new Set()): Set<string> {
  switch (cond.op) {
    case 'eq':
    case 'in':
      into.add(cond.attr);
      break;
    case 'not':
      conditionAttributes(cond.cond, into);
      break;
    case 'and':
    case 'or':
      for (const c of cond.conds) conditionAttributes(c, into);
      break;
  }
  return into;
}

export function policyAttributes(policy: Policy): Set<string> {
  const attrs = new Set<string>();
  for (const rule of policy.rules) conditionAttributes(rule.when, attrs);
  return attrs;
}

// ---------------------------------------------------------------------------
// 属性域与合法赋值
// ---------------------------------------------------------------------------

export function isLegalAssignment(domain: Domain, assignment: Assignment): boolean {
  for (const [attr, values] of Object.entries(domain.attributes)) {
    if (!values.includes(assignment[attr])) return false;
  }
  for (const combo of domain.incompatible ?? []) {
    const matches = Object.entries(combo).every(([attr, value]) => assignment[attr] === value);
    if (matches) return false;
  }
  return true;
}

/** 全枚举属性域内的所有合法赋值（适用于小属性域）。 */
export function enumerateLegalAssignments(domain: Domain): Assignment[] {
  const attrs = Object.keys(domain.attributes);
  const result: Assignment[] = [];
  const current: Record<string, AttrValue> = {};
  const walk = (index: number): void => {
    if (index === attrs.length) {
      if (isLegalAssignment(domain, current)) result.push({ ...current });
      return;
    }
    const attr = attrs[index];
    for (const value of domain.attributes[attr]) {
      current[attr] = value;
      walk(index + 1);
    }
    delete current[attr];
  };
  walk(0);
  return result;
}

// ---------------------------------------------------------------------------
// 证明
// ---------------------------------------------------------------------------

export interface ClaimProof {
  kind: 'claim';
  id: string;
  attr: string;
  value: AttrValue;
  cost: number;
  signature?: string;
}

export interface SetMembershipProof {
  kind: 'setMembership';
  id: string;
  attr: string;
  values: readonly AttrValue[];
  cost: number;
  signature?: string;
}

export type Proof = ClaimProof | SetMembershipProof;

/** 证明对属性取值施加的约束是否被给定赋值满足。 */
export function proofSatisfiedBy(proof: Proof, assignment: Assignment): boolean {
  if (proof.kind === 'claim') return assignment[proof.attr] === proof.value;
  return proof.values.includes(assignment[proof.attr]);
}

// ---------------------------------------------------------------------------
// 证明选择
// ---------------------------------------------------------------------------

export interface SelectionInput {
  domain: Domain;
  policy: Policy;
  /** 终端当前的真实属性（必须是合法赋值）。 */
  trueAttributes: Assignment;
  /** 终端持有的候选证明（含签名）。 */
  proofs: readonly Proof[];
  /** 已撤销的证明编号。 */
  revokedProofIds?: readonly string[];
  /** 可选的签名校验回调；返回 false 的证明视为不可出具。 */
  verifySignature?: (proof: Proof) => boolean;
}

export interface MinimalityWitness {
  /** 若移除该证明…… */
  removedProofId: string;
  /** ……则存在此合法赋值：与剩余证明一致，但授权结论相反，说明该证明不可再少。 */
  counterexample: Assignment;
}

export interface SufficiencyEvidence {
  decision: Effect;
  proofIds: string[];
  proofs: Proof[];
  minimality: MinimalityWitness[];
}

export interface SelectionResult {
  evidence: SufficiencyEvidence;
  totalCost: number;
  /** 披露的确值个数（claim 证明的数量）。 */
  disclosedValues: number;
}

function assertValidInput(input: SelectionInput): void {
  const attrs = Object.keys(input.domain.attributes);
  if (attrs.length === 0) throw new Error('domain 至少需要一个属性');
  for (const [attr, values] of Object.entries(input.domain.attributes)) {
    if (values.length === 0) throw new Error(`属性 ${attr} 的取值域为空`);
  }
  for (const combo of input.domain.incompatible ?? []) {
    for (const attr of Object.keys(combo)) {
      if (!(attr in input.domain.attributes)) {
        throw new Error(`不可成立组合引用了未知属性: ${attr}`);
      }
    }
  }
  if (!isLegalAssignment(input.domain, input.trueAttributes)) {
    throw new Error('trueAttributes 不是该属性域内的合法赋值');
  }
  const seen = new Set<string>();
  for (const proof of input.proofs) {
    if (seen.has(proof.id)) throw new Error(`证明编号重复: ${proof.id}`);
    seen.add(proof.id);
    if (!(proof.attr in input.domain.attributes)) {
      throw new Error(`证明 ${proof.id} 引用了未知属性: ${proof.attr}`);
    }
    if (!Number.isFinite(proof.cost) || proof.cost <= 0) {
      throw new Error(`证明 ${proof.id} 的代价必须为正数`);
    }
    if (proof.kind === 'setMembership' && proof.values.length === 0) {
      throw new Error(`证明 ${proof.id} 的成员集合为空`);
    }
  }
}

/** 与一组证明一致（且合法）的赋值。 */
export function consistentAssignments(domain: Domain, proofs: readonly Proof[]): Assignment[] {
  return enumerateLegalAssignments(domain).filter((a) => proofs.every((p) => proofSatisfiedBy(p, a)));
}

/** 比较证明编号序列（先按元素字典序，再按长度）。 */
function compareIdSequences(a: readonly string[], b: readonly string[]): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    if (a[i] < b[i]) return -1;
    if (a[i] > b[i]) return 1;
  }
  return a.length - b.length;
}

interface Candidate {
  proofs: Proof[];
  cost: number;
  claims: number;
  ids: string[];
}

function isBetterCandidate(next: Candidate, best: Candidate): boolean {
  if (next.cost !== best.cost) return next.cost < best.cost;
  if (next.claims !== best.claims) return next.claims < best.claims;
  return compareIdSequences(next.ids, best.ids) < 0;
}

/**
 * 为当前真实属性选择总代价最小的一组互相兼容的可出具证明，
 * 使所有与这些证明一致的合法赋值都得到相同授权结论。
 * 决胜顺序：总代价 → 披露确值个数 → 证明编号序列。
 * 不存在这样的证明组时返回 null。
 */
export function selectProofs(input: SelectionInput): SelectionResult | null {
  assertValidInput(input);
  const { domain, policy } = input;
  const revoked = new Set(input.revokedProofIds ?? []);

  // 可出具 = 未撤销、签名有效、且与真实属性一致（终端只能为真实现状出具证明）。
  const relevant = policyAttributes(policy);
  const issuable = input.proofs.filter(
    (p) =>
      !revoked.has(p.id) &&
      relevant.has(p.attr) &&
      proofSatisfiedBy(p, input.trueAttributes) &&
      (input.verifySignature ? input.verifySignature(p) : true),
  );

  const legal = enumerateLegalAssignments(domain);
  const decisions = legal.map((a) => decide(policy, a));

  // 判定一组证明是否充分：所有一致的合法赋值结论相同。
  const determines = (subset: readonly Proof[]): boolean => {
    let first: Effect | null = null;
    for (let i = 0; i < legal.length; i++) {
      const a = legal[i];
      if (!subset.every((p) => proofSatisfiedBy(p, a))) continue;
      if (first === null) first = decisions[i];
      else if (decisions[i] !== first) return false;
    }
    // 真实属性必然满足所有可出具证明，故一致集合非空。
    return first !== null;
  };

  // 分支限界枚举子集（代价为正，按当前最优代价剪枝）。
  const ordered = [...issuable].sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
  let best: Candidate | null = null;
  const chosen: Proof[] = [];

  const search = (index: number, cost: number, claims: number): void => {
    if (best !== null && cost > best.cost) return;
    if (index === ordered.length) {
      if (!determines(chosen)) return;
      const candidate: Candidate = {
        proofs: [...chosen],
        cost,
        claims,
        ids: chosen.map((p) => p.id).sort(),
      };
      if (best === null || isBetterCandidate(candidate, best)) best = candidate;
      return;
    }
    // 不选 ordered[index]
    search(index + 1, cost, claims);
    // 选 ordered[index]
    chosen.push(ordered[index]);
    search(index + 1, cost + ordered[index].cost, claims + (ordered[index].kind === 'claim' ? 1 : 0));
    chosen.pop();
  };
  search(0, 0, 0);

  if (best === null) return null;
  const winner: Candidate = best;
  const decision = decide(policy, input.trueAttributes);

  // 充分性依据：对选中的每个证明，给出一个合法赋值——它与剩余证明一致但结论相反，
  // 以此说明证明不可再少。最优性（正代价）保证这样的反例必然存在。
  const minimality: MinimalityWitness[] = winner.proofs.map((removed) => {
    const rest = winner.proofs.filter((p) => p.id !== removed.id);
    const counterexample = legal.find(
      (a) => rest.every((p) => proofSatisfiedBy(p, a)) && decide(policy, a) !== decision,
    );
    if (counterexample === undefined) {
      throw new Error(`内部错误：证明 ${removed.id} 冗余，与最优性矛盾`);
    }
    return { removedProofId: removed.id, counterexample };
  });

  return {
    evidence: {
      decision,
      proofIds: winner.ids,
      proofs: [...winner.proofs].sort((a, b) => (a.id < b.id ? -1 : 1)),
      minimality,
    },
    totalCost: winner.cost,
    disclosedValues: winner.claims,
  };
}

// ---------------------------------------------------------------------------
// 充分性依据的独立验证
// ---------------------------------------------------------------------------

/**
 * 独立验证充分性依据，不依赖选择器的内部逻辑：
 * 1. 证明组互相兼容（存在与全部证明一致的合法赋值）；
 * 2. 所有与证明一致的合法赋值都得到 evidence.decision（充分性）；
 * 3. 每个证明都附有反例：移除它后存在与剩余证明一致、结论相反的合法赋值（极小性）。
 */
export function verifyEvidence(domain: Domain, policy: Policy, evidence: SufficiencyEvidence): boolean {
  const { proofs, minimality } = evidence;

  const ids = proofs.map((p) => p.id);
  if (new Set(ids).size !== ids.length) return false;
  if ([...ids].sort().join(',') !== [...evidence.proofIds].sort().join(',')) return false;

  const legal = enumerateLegalAssignments(domain);
  const consistent = legal.filter((a) => proofs.every((p) => proofSatisfiedBy(p, a)));
  if (consistent.length === 0) return false; // 互不兼容
  if (!consistent.every((a) => decide(policy, a) === evidence.decision)) return false;

  const witnessed = new Set<string>();
  for (const witness of minimality) {
    const rest = proofs.filter((p) => p.id !== witness.removedProofId);
    if (rest.length !== proofs.length - 1) return false; // 引用了未选中的证明
    const c = witness.counterexample;
    if (!isLegalAssignment(domain, c)) return false;
    if (!rest.every((p) => proofSatisfiedBy(p, c))) return false;
    if (decide(policy, c) === evidence.decision) return false;
    witnessed.add(witness.removedProofId);
  }
  return proofs.every((p) => witnessed.has(p.id));
}

/**
 * 可逆事实演绎推理引擎。
 *
 * 基于霍恩子句的正向链推理，内部采用“理由维护系统”(justification-based TMS)：
 * 每个事实记录其全部正当理由（justification），只要存在至少一条
 * 当前有效的理由即成立；当基础事实被撤销时，级联清除失去全部
 * 独立证据的派生结论。
 */

export interface Rule {
  id: string;
  /** 前件（可空，空数组表示无条件成立的结论） */
  antecedents: string[];
  /** 后件 */
  conclusion: string;
}

interface Justification {
  /** null 表示基础事实（公理） */
  ruleId: string | null;
  antecedents: string[];
}

interface FactRecord {
  name: string;
  justifications: Justification[];
}

export interface ProofNode {
  fact: string;
  /** null 表示基础事实 */
  ruleId: string | null;
  children: ProofNode[];
}

export interface Explanation {
  /** 最短证明树 */
  proof: ProofNode;
  /** 最小基础事实证据集 */
  baseFacts: string[];
  /** 证明树深度（推理步数） */
  depth: number;
}

export class ReasoningEngine {
  private facts = new Map<string, FactRecord>();
  private rules = new Map<string, Rule>();
  private rulesByAntecedent = new Map<string, Set<string>>();
  /** 当前成立（in）的事实集合 */
  private valid = new Set<string>();
  private ruleSeq = 0;

  /** 断言一个基础事实。重复断言为幂等操作。 */
  addFact(name: string): void {
    const rec = this.record(name);
    if (rec.justifications.some((j) => j.ruleId === null)) return;
    rec.justifications.push({ ruleId: null, antecedents: [] });
    this.markValid(name);
  }

  /** 添加一条霍恩规则：IF antecedents THEN conclusion。返回规则 id。 */
  addRule(antecedents: string[], conclusion: string): string {
    const id = `r${++this.ruleSeq}`;
    const rule: Rule = { id, antecedents: [...antecedents], conclusion };
    this.rules.set(id, rule);
    for (const a of rule.antecedents) {
      let set = this.rulesByAntecedent.get(a);
      if (!set) this.rulesByAntecedent.set(a, (set = new Set()));
      set.add(id);
    }
    this.record(conclusion);
    this.tryFire(rule);
    return id;
  }

  /** 撤销一个基础事实，并级联清除失去全部支持的派生结论。 */
  retractFact(name: string): void {
    const rec = this.facts.get(name);
    if (!rec) return;
    const idx = rec.justifications.findIndex((j) => j.ruleId === null);
    if (idx === -1) return; // 不是基础事实，忽略
    rec.justifications.splice(idx, 1);
    this.recomputeClosure();
  }

  /**
   * 从剩余基础事实出发重算最小不动点闭包。
   * 只有从基础事实良基（well-founded）可达的事实才保持成立，
   * 因此无根基的循环互证（如 A->B, B->A）也会被正确清除。
   */
  private recomputeClosure(): void {
    const next = new Set<string>();
    let changed = true;
    while (changed) {
      changed = false;
      for (const rec of this.facts.values()) {
        if (next.has(rec.name)) continue;
        if (
          rec.justifications.some((j) =>
            j.antecedents.every((a) => next.has(a)),
          )
        ) {
          next.add(rec.name);
          changed = true;
        }
      }
    }
    this.valid = next;
  }

  /** 事实当前是否成立。 */
  isKnown(name: string): boolean {
    return this.valid.has(name);
  }

  /** 当前成立的全部事实。 */
  knownFacts(): string[] {
    return [...this.valid].sort();
  }

  /**
   * 查询派生结论的最小解释：返回使用基础事实最少
   * （并列时推理深度最浅）的最短证据链证明树。
   * 事实不成立时返回 null。
   */
  explain(name: string): Explanation | null {
    if (!this.valid.has(name)) return null;
    const memo = new Map<string, Explanation | null>();
    return this.bestProof(name, memo, new Set());
  }

  private record(name: string): FactRecord {
    let rec = this.facts.get(name);
    if (!rec) this.facts.set(name, (rec = { name, justifications: [] }));
    return rec;
  }

  private markValid(name: string): void {
    if (this.valid.has(name)) return;
    this.valid.add(name);
    for (const ruleId of this.rulesByAntecedent.get(name) ?? []) {
      this.tryFire(this.rules.get(ruleId)!);
    }
  }

  private tryFire(rule: Rule): void {
    if (!rule.antecedents.every((a) => this.valid.has(a))) return;
    const rec = this.record(rule.conclusion);
    if (!rec.justifications.some((j) => j.ruleId === rule.id)) {
      rec.justifications.push({ ruleId: rule.id, antecedents: rule.antecedents });
    }
    this.markValid(rule.conclusion);
  }

  private bestProof(
    name: string,
    memo: Map<string, Explanation | null>,
    visiting: Set<string>,
  ): Explanation | null {
    const hit = memo.get(name);
    if (hit !== undefined) return hit;
    if (visiting.has(name)) return null; // 打破规则环
    visiting.add(name);

    const rec = this.facts.get(name)!;
    let best: Explanation | null = null;
    for (const j of rec.justifications) {
      if (!j.antecedents.every((a) => this.valid.has(a))) continue;
      const children: ProofNode[] = [];
      const baseFacts = new Set<string>();
      let depth = 0;
      let ok = true;
      for (const a of j.antecedents) {
        const sub = this.bestProof(a, memo, visiting);
        if (!sub) {
          ok = false;
          break;
        }
        children.push(sub.proof);
        for (const b of sub.baseFacts) baseFacts.add(b);
        depth = Math.max(depth, sub.depth);
      }
      if (!ok) continue;
      if (j.ruleId === null) baseFacts.add(name);
      const candidate: Explanation = {
        proof: { fact: name, ruleId: j.ruleId, children },
        baseFacts: [...baseFacts].sort(),
        depth: j.ruleId === null ? 0 : depth + 1,
      };
      if (
        !best ||
        candidate.baseFacts.length < best.baseFacts.length ||
        (candidate.baseFacts.length === best.baseFacts.length &&
          candidate.depth < best.depth)
      ) {
        best = candidate;
      }
    }

    visiting.delete(name);
    memo.set(name, best);
    return best;
  }
}

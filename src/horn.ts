/**
 * 可逆事实演绎推理与最小解释生成器
 *
 * 基于霍恩子句的正向链推理器：
 *  - 从已知事实推导新结论（forward chaining）
 *  - 推导过程建立证明树（每条派生事实记录其全部正当理由）
 *  - 支持查询任意派生结论的最短事实证据链（最小解释）
 *  - 基础事实被推翻时，级联清除失去全部支持的派生结论（真值维护）
 */

export interface Rule {
  /** 规则唯一标识 */
  id: string;
  /** 前提（合取），空数组表示无条件成立 */
  premises: string[];
  /** 结论 */
  conclusion: string;
}

/** 一条正当理由：某规则以其前提集合推出了结论 */
export interface Justification {
  ruleId: string;
  premises: string[];
}

/** 证据链中的一步推导 */
export interface DerivationStep {
  conclusion: string;
  ruleId: string;
  premises: string[];
}

/** 最小解释（最短事实证据链） */
export interface Explanation {
  /** 被解释的结论 */
  fact: string;
  /** 推导深度（基础事实为 0，每经过一次规则推导 +1） */
  depth: number;
  /** 该结论最终依赖的全部基础事实 */
  baseFacts: string[];
  /** 按拓扑序排列的推导步骤，最后一步推出 fact */
  steps: DerivationStep[];
}

export class Reasoner {
  private readonly rules = new Map<string, Rule>();
  /** 基础事实（被显式断言、未被推翻） */
  private readonly asserted = new Set<string>();
  /** 派生事实的正当理由集合：fact -> 支持它的正当推导 */
  private readonly supports = new Map<string, Set<Justification>>();
  /** 当前成立的全部事实（基础 + 派生） */
  private readonly known = new Set<string>();

  // ---------------------------------------------------------------- 规则

  /** 添加霍恩规则；若前提当前全部成立则立即触发正向链推导 */
  addRule(rule: Rule): void {
    if (this.rules.has(rule.id)) {
      throw new Error(`duplicate rule id: ${rule.id}`);
    }
    this.rules.set(rule.id, rule);
    this.tryFire(rule);
  }

  // ---------------------------------------------------------------- 断言

  /** 断言一个基础事实，并触发正向链推导 */
  assert(fact: string): void {
    if (this.asserted.has(fact)) return;
    this.asserted.add(fact);
    this.introduce(fact);
  }

  /** 撤销一个基础事实；失去全部支持的派生事实被级联清除 */
  retract(fact: string): void {
    if (!this.asserted.delete(fact)) return; // 不是基础事实，无需处理
    if (this.isSupported(fact)) return;      // 仍有独立推导支持，保留
    this.remove(fact);
  }

  // ---------------------------------------------------------------- 查询

  /** 事实当前是否成立（基础或派生） */
  knows(fact: string): boolean {
    return this.known.has(fact);
  }

  /** 当前成立的全部事实 */
  facts(): string[] {
    return [...this.known].sort();
  }

  /** 事实的全部直接正当理由（证明树的父边） */
  justificationsOf(fact: string): Justification[] {
    return [...(this.supports.get(fact) ?? [])];
  }

  /**
   * 最小解释：返回派生结论的最短事实证据链。
   * 若事实不成立返回 null；基础事实的深度为 0，证据即其自身。
   */
  explain(fact: string): Explanation | null {
    if (!this.known.has(fact)) return null;

    const depthMemo = new Map<string, number>();
    const inProgress = new Set<string>();

    const depthOf = (f: string): number => {
      if (this.asserted.has(f)) return 0;
      const memo = depthMemo.get(f);
      if (memo !== undefined) return memo;
      if (inProgress.has(f)) return Infinity; // 循环证明不可作为证据
      inProgress.add(f);
      let best = Infinity;
      for (const j of this.supports.get(f) ?? []) {
        let worst = 0;
        for (const p of j.premises) {
          const d = depthOf(p);
          if (d > worst) worst = d;
        }
        if (worst + 1 < best) best = worst + 1;
      }
      inProgress.delete(f);
      depthMemo.set(f, best);
      return best;
    };

    const depth = depthOf(fact);
    if (!Number.isFinite(depth)) return null;

    // 沿最优正当理由回溯，收集推导步骤与基础事实
    const steps: DerivationStep[] = [];
    const baseFacts = new Set<string>();
    const visited = new Set<string>();

    const walk = (f: string): void => {
      if (this.asserted.has(f)) {
        baseFacts.add(f);
        return;
      }
      if (visited.has(f)) return;
      visited.add(f);
      const target = depthMemo.get(f)!;
      for (const j of this.supports.get(f) ?? []) {
        const worst = Math.max(0, ...j.premises.map((p) => depthMemo.get(p) ?? 0));
        if (worst + 1 === target) {
          for (const p of j.premises) walk(p);
          steps.push({ conclusion: f, ruleId: j.ruleId, premises: [...j.premises] });
          return;
        }
      }
    };
    walk(fact);

    return { fact, depth, baseFacts: [...baseFacts].sort(), steps };
  }

  // ---------------------------------------------------------------- 内部

  /** 事实是否有至少一条有效的推导支持 */
  private isSupported(fact: string): boolean {
    return (this.supports.get(fact)?.size ?? 0) > 0;
  }

  /** 新事实进入知识库，并继续正向链传播 */
  private introduce(fact: string): void {
    const isNew = !this.known.has(fact);
    this.known.add(fact);
    if (!isNew) return;
    for (const rule of this.rules.values()) {
      if (rule.premises.includes(fact)) this.tryFire(rule);
    }
  }

  /** 若规则前提全部成立，则为结论登记一条正当理由 */
  private tryFire(rule: Rule): void {
    if (!rule.premises.every((p) => this.known.has(p))) return;
    const just: Justification = { ruleId: rule.id, premises: [...rule.premises] };
    let set = this.supports.get(rule.conclusion);
    if (!set) {
      set = new Set();
      this.supports.set(rule.conclusion, set);
    }
    for (const existing of set) {
      if (existing.ruleId === just.ruleId) return; // 同一规则的证明不重复登记
    }
    set.add(just);
    this.introduce(rule.conclusion);
  }

  /** 移除事实，并级联清除依赖它且失去全部支持的派生事实 */
  private remove(fact: string): void {
    this.known.delete(fact);
    this.supports.delete(fact);
    for (const rule of this.rules.values()) {
      if (!rule.premises.includes(fact)) continue;
      const set = this.supports.get(rule.conclusion);
      if (!set) continue;
      for (const j of set) {
        if (j.ruleId === rule.id) set.delete(j);
      }
      if (set.size === 0) {
        this.supports.delete(rule.conclusion);
        if (!this.asserted.has(rule.conclusion) && this.known.has(rule.conclusion)) {
          this.remove(rule.conclusion);
        }
      }
    }
  }
}

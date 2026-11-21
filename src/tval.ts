/**
 * 污点事实图与带污点标记的运行时值。
 *
 * 设计要点：
 * - 每个外部输入在事实图中登记一个 origin 事实（污点来源）。
 * - 每个传播算子生成一个 link 事实，记录其父事实与操作类型，
 *   父事实可以有多个（例如拼接两侧都是污点），从而构成 DAG。
 * - TVal 是“值 + 事实 id 列表”的容器，事实 id 为空表示已清洗/干净。
 */

export type Literal = string | number | boolean | undefined;

export interface OriginFact {
  kind: "origin";
  id: number;
  source: string;
  raw: Literal;
}

export interface LinkFact {
  kind: "link";
  id: number;
  op: string;
  detail: string;
  parents: number[];
}

export type TaintFact = OriginFact | LinkFact;

export interface ChainStep {
  factId: number;
  label: string;
  parents: number[];
}

export class TaintRegistry {
  private facts = new Map<number, TaintFact>();
  private seq = 0;

  origin(source: string, raw: Literal): number {
    const id = ++this.seq;
    this.facts.set(id, { kind: "origin", id, source, raw });
    return id;
  }

  link(op: string, detail: string, parents: number[]): number | undefined {
    const uniqueParents = [...new Set(parents)];
    if (uniqueParents.length === 0) {
      return undefined;
    }
    const id = ++this.seq;
    this.facts.set(id, { kind: "link", id, op, detail, parents: uniqueParents });
    return id;
  }

  fact(id: number): TaintFact {
    const fact = this.facts.get(id);
    if (!fact) {
      throw new Error(`unknown taint fact #${id}`);
    }
    return fact;
  }

  /** 沿第一个父事实回溯出一条完整来源链。 */
  chainOf(factId: number): ChainStep[] {
    const steps: ChainStep[] = [];
    const seen = new Set<number>();
    let current = factId;
    while (!seen.has(current)) {
      seen.add(current);
      const fact = this.fact(current);
      steps.push({
        factId: fact.id,
        label: this.describe(fact),
        parents: fact.kind === "link" ? fact.parents : [],
      });
      if (fact.kind === "origin" || fact.parents.length === 0) {
        break;
      }
      current = fact.parents[0];
    }
    return steps;
  }

  /** 收集事实可达的全部 origin（用于列出所有污点根来源）。 */
  originsOf(factIds: number[]): OriginFact[] {
    const roots: OriginFact[] = [];
    const seen = new Set<number>();
    const visit = (id: number): void => {
      if (seen.has(id)) {
        return;
      }
      seen.add(id);
      const fact = this.fact(id);
      if (fact.kind === "origin") {
        roots.push(fact);
        return;
      }
      for (const parent of fact.parents) {
        visit(parent);
      }
    };
    factIds.forEach(visit);
    return roots;
  }

  private describe(fact: TaintFact): string {
    if (fact.kind === "origin") {
      const preview = previewValue(fact.raw);
      return `origin(${fact.source}${preview === "" ? "" : `=${preview}`})`;
    }
    return `${fact.op}(${fact.detail})`;
  }
}

/** 截断并展示字面值，方便来源链阅读。 */
export function previewValue(value: Literal): string {
  if (value === undefined) {
    return "";
  }
  const text = String(value);
  return text.length > 24 ? `${text.slice(0, 24)}...` : text;
}

/**
 * 带污点标记的值。factIds 为空数组表示干净，
 * 非空时每个 id 都指向事实图中的一个污点事实。
 */
export class TVal<T extends Literal = Literal> {
  readonly raw: T;
  readonly factIds: readonly number[];
  readonly registry: TaintRegistry;

  constructor(raw: T, factIds: readonly number[], registry: TaintRegistry) {
    this.raw = raw;
    this.factIds = factIds;
    this.registry = registry;
  }

  get isTainted(): boolean {
    return this.factIds.length > 0;
  }

  /** 主来源链：沿主事实的第一个父节点回溯。 */
  chain(): ChainStep[] {
    if (this.factIds.length === 0) {
      return [];
    }
    return this.registry.chainOf(this.factIds[0]);
  }

  /** 该值可达的全部污点根来源。 */
  roots(): OriginFact[] {
    return this.registry.originsOf([...this.factIds]);
  }

  /** 人类可读的来源链字符串（ -> 串联）。 */
  chainString(): string {
    return this.chain()
      .map((step) => step.label)
      .join(" -> ");
  }
}

export type Val = TVal | Literal;

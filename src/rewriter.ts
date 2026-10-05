import {
  Bindings,
  Factory,
  JsonTerm,
  Node,
  Pattern,
  fromJson,
  instantiate,
  match,
  pretty,
  toJson,
} from "./terms";

// 规则：priority 数值小者优先尝试；同优先级按声明顺序。
export interface Rule {
  readonly name: string;
  readonly priority: number;
  readonly lhs: Pattern;
  readonly rhs: Pattern;
}

export interface TraceStep {
  readonly step: number;
  readonly path: readonly number[]; // 从根到命中节点的子节点下标路径（按树展开语义）
  readonly rule: string;
  readonly redex: string; // 命中节点的打印形式
  readonly bindings: Record<string, string>; // 变量 -> 绑定子树的打印形式
  readonly replacement: string; // 替换式实例化结果的打印形式
}

export type Status = "running" | "normal-form" | "cycle" | "budget";

// 重写状态：可在预算中断后原样恢复（甚至序列化到磁盘再恢复）。
export interface State {
  readonly root: Node;
  readonly step: number; // 已完成步数；当前项即"第 step 步之前的状态"
  readonly seen: Map<number, number>; // 状态结构哈希(node.id) -> 首次出现的步数
  readonly status: Status;
  readonly cycle?: { readonly from: number; readonly to: number };
}

interface Redex {
  readonly path: number[];
  readonly node: Node;
  readonly rule: Rule;
  readonly bindings: Bindings;
}

export class Rewriter {
  private readonly ordered: Rule[];

  constructor(private readonly factory: Factory, rules: readonly Rule[]) {
    this.ordered = rules
      .map((rule, index) => ({ rule, index }))
      .sort((a, b) => a.rule.priority - b.rule.priority || a.index - b.index)
      .map((e) => e.rule);
  }

  init(root: Node): State {
    return { root, step: 0, seen: new Map(), status: "running" };
  }

  // 最左最内层：先按从左到右顺序在子树中找 redex，全部不可约时才尝试当前节点。
  private findRedex(node: Node, path: number[]): Redex | null {
    for (let i = 0; i < node.args.length; i++) {
      const hit = this.findRedex(node.args[i], [...path, i]);
      if (hit) return hit;
    }
    for (const rule of this.ordered) {
      const bindings = match(rule.lhs, node);
      if (bindings) return { path, node, rule, bindings };
    }
    return null;
  }

  // 路径复制：只重建根到 redex 的脊柱，未触及的子树按引用共享。
  private replaceAt(node: Node, path: readonly number[], replacement: Node): Node {
    if (path.length === 0) return replacement;
    const args = node.args.slice();
    args[path[0]] = this.replaceAt(node.args[path[0]], path.slice(1), replacement);
    return this.factory.mk(node.sym, args);
  }

  // 最多执行 budget 步。返回新状态与本轮轨迹；
  // 状态带 "budget" 时可用更大预算继续 run() 恢复执行。
  run(state: State, budget: number): { state: State; steps: TraceStep[] } {
    if (state.status !== "running" && state.status !== "budget") {
      return { state, steps: [] };
    }
    const steps: TraceStep[] = [];
    let root = state.root;
    let step = state.step;
    const seen = state.seen;

    while (steps.length < budget) {
      const previous = seen.get(root.id);
      if (previous !== undefined) {
        return {
          state: { root, step, seen, status: "cycle", cycle: { from: previous, to: step } },
          steps,
        };
      }
      seen.set(root.id, step);

      const redex = this.findRedex(root, []);
      if (!redex) {
        return { state: { root, step, seen, status: "normal-form" }, steps };
      }

      const replacement = instantiate(this.factory, redex.rule.rhs, redex.bindings);
      const bindings: Record<string, string> = {};
      for (const [name, value] of redex.bindings) bindings[name] = pretty(value);
      steps.push({
        step,
        path: redex.path,
        rule: redex.rule.name,
        redex: pretty(redex.node),
        bindings,
        replacement: pretty(replacement),
      });

      root = this.replaceAt(root, redex.path, replacement);
      step++;
    }
    return { state: { root, step, seen, status: "budget" }, steps };
  }
}

// ---------- 状态快照：预算中断可跨进程恢复 ----------

export interface StateSnapshot {
  readonly root: JsonTerm;
  readonly step: number;
  readonly seen: ReadonlyArray<{ term: JsonTerm; step: number }>;
}

export function exportState(factory: Factory, state: State): StateSnapshot {
  const seen: Array<{ term: JsonTerm; step: number }> = [];
  for (const [id, step] of state.seen) {
    const node = factory.byId(id);
    if (node) seen.push({ term: toJson(node), step });
  }
  return { root: toJson(state.root), step: state.step, seen };
}

export function importState(factory: Factory, snap: StateSnapshot): State {
  const seen = new Map<number, number>();
  for (const entry of snap.seen) {
    seen.set(fromJson(factory, entry.term).id, entry.step);
  }
  return { root: fromJson(factory, snap.root), step: snap.step, seen, status: "running" };
}

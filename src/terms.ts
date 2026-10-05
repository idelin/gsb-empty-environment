// 项（Term）以不可变 DAG 节点表示，工厂做 hash-consing：
// 结构相同的子树在全工厂范围内就是同一个对象，因此
//   1) 共享子树天然安全 —— 重写只走"路径复制"，从不原地修改；
//   2) 结构相等等价于引用相等，匹配与状态哈希都是 O(1) 比较。

export interface Node {
  readonly id: number; // 工厂内唯一，hash-cons 结果；可当作结构哈希使用
  readonly sym: string;
  readonly args: readonly Node[];
}

export class Factory {
  private table = new Map<string, Node>();
  private nodes: Node[] = [];

  mk(sym: string, args: readonly Node[] = []): Node {
    const key = sym + " " + args.map((a) => a.id).join(" ");
    const hit = this.table.get(key);
    if (hit) return hit;
    const node: Node = { id: this.nodes.length, sym, args: Object.freeze(args.slice()) };
    this.table.set(key, node);
    this.nodes.push(node);
    return node;
  }

  byId(id: number): Node | undefined {
    return this.nodes[id];
  }
}

// ---------- 模式 / 替换式 ----------

export type Pattern =
  | { readonly kind: "var"; readonly name: string }
  | { readonly kind: "node"; readonly sym: string; readonly args: readonly Pattern[] };

export const v = (name: string): Pattern => ({ kind: "var", name });
export const p = (sym: string, ...args: Pattern[]): Pattern => ({ kind: "node", sym, args });

export type Bindings = Map<string, Node>;

// 一阶匹配：pat 中的变量绑定到 node 的（共享）子树。
// 同名变量多次出现时必须绑定到结构相同的子树；hash-consing 下用 === 判断即可。
export function match(pat: Pattern, node: Node, out: Bindings = new Map()): Bindings | null {
  if (pat.kind === "var") {
    const prev = out.get(pat.name);
    if (prev === undefined) {
      out.set(pat.name, node);
      return out;
    }
    return prev === node ? out : null;
  }
  if (pat.sym !== node.sym || pat.args.length !== node.args.length) return null;
  for (let i = 0; i < pat.args.length; i++) {
    if (!match(pat.args[i], node.args[i], out)) return null;
  }
  return out;
}

// 用绑定实例化替换式。变量对应的子树按引用复用 —— 这是"逻辑复制"：
// 新项与旧项共享子结构，但由于节点不可变，任何后续重写都不会污染共享方。
export function instantiate(factory: Factory, tmpl: Pattern, env: Bindings): Node {
  if (tmpl.kind === "var") {
    const bound = env.get(tmpl.name);
    if (!bound) throw new Error(`unbound variable in template: ${tmpl.name}`);
    return bound;
  }
  return factory.mk(
    tmpl.sym,
    tmpl.args.map((a) => instantiate(factory, a, env)),
  );
}

// ---------- 打印 / 序列化 ----------

export function pretty(node: Node): string {
  if (node.args.length === 0) return node.sym;
  return `${node.sym}(${node.args.map(pretty).join(", ")})`;
}

export interface JsonTerm {
  s: string;
  a: JsonTerm[];
}

export function toJson(node: Node): JsonTerm {
  return { s: node.sym, a: node.args.map(toJson) };
}

export function fromJson(factory: Factory, j: JsonTerm): Node {
  return factory.mk(j.s, j.a.map((c) => fromJson(factory, c)));
}

/**
 * Condition language for policy applicability.
 *
 * Policies may only read explicit attributes. The language is deliberately
 * restricted to finite-set predicates, integer conditions (including
 * difference constraints between two integer variables) and boolean
 * combinations. There is no way to call external identity services.
 */

export type Scalar = string | number | boolean;

export type CmpOp = "<" | "<=" | "==" | "!=" | ">=" | ">";

export type Cond =
  | { tag: "true" }
  | { tag: "false" }
  | { tag: "in"; attr: string; values: readonly Scalar[] }
  | { tag: "cmp"; attr: string; op: CmpOp; value: number }
  | { tag: "diff"; left: string; right: string; op: CmpOp; value: number }
  | { tag: "setHas"; attr: string; elem: Scalar }
  | { tag: "setSubset"; attr: string; values: readonly Scalar[] }
  | { tag: "setIntersects"; attr: string; values: readonly Scalar[] }
  | { tag: "not"; of: Cond }
  | { tag: "and"; args: readonly Cond[] }
  | { tag: "or"; args: readonly Cond[] };

const scalarKey = (v: Scalar): string => `${typeof v}:${String(v)}`;

/**
 * Hash-consing factory: structurally identical conditions share one canonical
 * node (the shared decision representation). Boolean nodes are normalized
 * (flattened, sorted, deduplicated, trivial identities folded) so sharing is
 * maximized across independently built policies.
 */
export class ConditionFactory {
  private table = new Map<string, Cond>();
  private ids = new Map<Cond, number>();
  private counter = 0;

  private intern(key: string, node: Cond): Cond {
    const hit = this.table.get(key);
    if (hit !== undefined) return hit;
    this.table.set(key, node);
    this.ids.set(node, this.counter);
    this.counter += 1;
    return node;
  }

  /** Unique id of a canonical node built by this factory. */
  id(c: Cond): number {
    const v = this.ids.get(c);
    if (v === undefined) {
      throw new Error("condition was not built by this ConditionFactory");
    }
    return v;
  }

  /** Number of distinct canonical nodes allocated so far. */
  get nodeCount(): number {
    return this.counter;
  }

  key(c: Cond): string {
    switch (c.tag) {
      case "true":
        return "T";
      case "false":
        return "F";
      case "in":
        return `in(${c.attr}|${c.values.map(scalarKey).join(",")})`;
      case "cmp":
        return `cmp(${c.attr}${c.op}${c.value})`;
      case "diff":
        return `diff(${c.left}-${c.right}${c.op}${c.value})`;
      case "setHas":
        return `has(${c.attr}|${scalarKey(c.elem)})`;
      case "setSubset":
        return `sub(${c.attr}|${c.values.map(scalarKey).join(",")})`;
      case "setIntersects":
        return `ints(${c.attr}|${c.values.map(scalarKey).join(",")})`;
      case "not":
        return `not(${this.key(c.of)})`;
      case "and":
        return `and(${c.args.map((a) => this.key(a)).join(",")})`;
      case "or":
        return `or(${c.args.map((a) => this.key(a)).join(",")})`;
    }
  }

  true_(): Cond {
    return this.intern("T", { tag: "true" });
  }

  false_(): Cond {
    return this.intern("F", { tag: "false" });
  }

  in(attr: string, values: readonly Scalar[]): Cond {
    const uniq = [...new Set(values)].sort((a, b) =>
      scalarKey(a) < scalarKey(b) ? -1 : 1,
    );
    return this.intern(`in(${attr}|${uniq.map(scalarKey).join(",")})`, {
      tag: "in",
      attr,
      values: uniq,
    });
  }

  cmp(attr: string, op: CmpOp, value: number): Cond {
    return this.intern(`cmp(${attr}${op}${value})`, {
      tag: "cmp",
      attr,
      op,
      value,
    });
  }

  /** Difference constraint: left - right <op> value. */
  diff(left: string, right: string, op: CmpOp, value: number): Cond {
    return this.intern(`diff(${left}-${right}${op}${value})`, {
      tag: "diff",
      left,
      right,
      op,
      value,
    });
  }

  setHas(attr: string, elem: Scalar): Cond {
    return this.intern(`has(${attr}|${scalarKey(elem)})`, {
      tag: "setHas",
      attr,
      elem,
    });
  }

  setSubset(attr: string, values: readonly Scalar[]): Cond {
    const uniq = [...new Set(values)];
    return this.intern(`sub(${attr}|${uniq.map(scalarKey).join(",")})`, {
      tag: "setSubset",
      attr,
      values: uniq,
    });
  }

  setIntersects(attr: string, values: readonly Scalar[]): Cond {
    const uniq = [...new Set(values)];
    return this.intern(`ints(${attr}|${uniq.map(scalarKey).join(",")})`, {
      tag: "setIntersects",
      attr,
      values: uniq,
    });
  }

  not(of: Cond): Cond {
    if (of.tag === "true") return this.false_();
    if (of.tag === "false") return this.true_();
    if (of.tag === "not") return of.of;
    return this.intern(`not(${this.key(of)})`, { tag: "not", of });
  }

  and(args: readonly Cond[]): Cond {
    return this.boolean("and", args);
  }

  or(args: readonly Cond[]): Cond {
    return this.boolean("or", args);
  }

  private boolean(tag: "and" | "or", args: readonly Cond[]): Cond {
    const flat: Cond[] = [];
    const absorb = tag === "and" ? "false" : "true";
    const identity = tag === "and" ? "true" : "false";
    const walk = (list: readonly Cond[]): void => {
      for (const a of list) {
        if (a.tag === absorb) {
          flat.length = 0;
          flat.push(a);
          return;
        }
        if (a.tag === identity) continue;
        if (a.tag === tag) walk(a.args);
        else flat.push(a);
      }
    };
    walk(args);
    if (flat.length === 0) return tag === "and" ? this.true_() : this.false_();
    if (flat[0].tag === absorb) return flat[0];
    const byKey = new Map<string, Cond>();
    for (const c of flat) byKey.set(this.key(c), c);
    const uniq = [...byKey.values()].sort((a, b) =>
      this.key(a) < this.key(b) ? -1 : 1,
    );
    if (uniq.length === 1) return uniq[0];
    const key = `${tag}(${uniq.map((c) => this.key(c)).join(",")})`;
    return this.intern(key, { tag, args: uniq });
  }
}

/** Attributes read by a condition (policies only read explicit attributes). */
export function attrsOf(c: Cond, into: Set<string> = new Set()): Set<string> {
  switch (c.tag) {
    case "true":
    case "false":
      break;
    case "in":
    case "cmp":
    case "setHas":
    case "setSubset":
    case "setIntersects":
      into.add(c.attr);
      break;
    case "diff":
      into.add(c.left);
      into.add(c.right);
      break;
    case "not":
      attrsOf(c.of, into);
      break;
    case "and":
    case "or":
      for (const a of c.args) attrsOf(a, into);
      break;
  }
  return into;
}

export function condToString(c: Cond): string {
  switch (c.tag) {
    case "true":
      return "true";
    case "false":
      return "false";
    case "in":
      return `${c.attr} in {${c.values.map(String).join(", ")}}`;
    case "cmp":
      return `${c.attr} ${c.op} ${c.value}`;
    case "diff":
      return `${c.left} - ${c.right} ${c.op} ${c.value}`;
    case "setHas":
      return `${c.attr} has ${String(c.elem)}`;
    case "setSubset":
      return `${c.attr} subsetOf {${c.values.map(String).join(", ")}}`;
    case "setIntersects":
      return `${c.attr} intersects {${c.values.map(String).join(", ")}}`;
    case "not":
      return `not(${condToString(c.of)})`;
    case "and":
      return `(${c.args.map(condToString).join(" and ")})`;
    case "or":
      return `(${c.args.map(condToString).join(" or ")})`;
  }
}

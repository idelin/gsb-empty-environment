/**
 * Fixed-order reduced ordered binary decision diagram (ROBDD).
 *
 * Switch variables are declared once in a fixed order; every derived
 * condition is a BDD node. Reachability is computed symbolically on these
 * nodes, so switch assignments are never enumerated.
 */

export type BDDNode = number;

interface Internal {
  variable: number;
  low: BDDNode;
  high: BDDNode;
}

export type GateExpr =
  | { kind: 'const'; value: boolean }
  | { kind: 'var'; name: string }
  | { kind: 'not'; arg: GateExpr }
  | { kind: 'and'; args: GateExpr[] }
  | { kind: 'or'; args: GateExpr[] };

export const gate = {
  constOf(value: boolean): GateExpr {
    return { kind: 'const', value };
  },
  varOf(name: string): GateExpr {
    return { kind: 'var', name };
  },
  not(arg: GateExpr): GateExpr {
    return { kind: 'not', arg };
  },
  and(...args: GateExpr[]): GateExpr {
    return { kind: 'and', args };
  },
  or(...args: GateExpr[]): GateExpr {
    return { kind: 'or', args };
  },
};

export class FixedOrderBDD {
  readonly FALSE: BDDNode = 0;
  readonly TRUE: BDDNode = 1;

  private readonly varIndex = new Map<string, number>();
  private readonly nodes: Internal[] = [];
  private readonly cache = new Map<string, BDDNode>();
  private readonly iteCache = new Map<string, BDDNode>();

  readonly variables: readonly string[];

  constructor(variables: readonly string[]) {
    this.variables = variables;
    variables.forEach((name, index) => this.varIndex.set(name, index));
  }

  indexOf(name: string): number {
    const index = this.varIndex.get(name);
    if (index === undefined) {
      throw new Error(`Unknown BDD switch variable: ${name}`);
    }
    return index;
  }

  literal(name: string): BDDNode {
    const variable = this.indexOf(name);
    return this.mk(variable, this.FALSE, this.TRUE);
  }

  not(a: BDDNode): BDDNode {
    return this.ite(a, this.FALSE, this.TRUE);
  }

  and(a: BDDNode, b: BDDNode): BDDNode {
    return this.ite(a, b, this.FALSE);
  }
  andAll(parts: readonly BDDNode[]): BDDNode {
    return parts.reduce((acc, part) => this.and(acc, part), this.TRUE);
  }

  or(a: BDDNode, b: BDDNode): BDDNode {
    return this.ite(a, this.TRUE, b);
  }
  orAll(parts: readonly BDDNode[]): BDDNode {
    return parts.reduce((acc, part) => this.or(acc, part), this.FALSE);
  }

  /** Logical implication a => b. */
  implies(a: BDDNode, b: BDDNode): BDDNode {
    return this.or(this.not(a), b);
  }

  /** Every assignment satisfying `a` also satisfies `b`. */
  entails(a: BDDNode, b: BDDNode): boolean {
    return this.and(a, this.not(b)) === this.FALSE;
  }

  isSat(a: BDDNode): boolean {
    return a !== this.FALSE;
  }

  isUnsat(a: BDDNode): boolean {
    return a === this.FALSE;
  }

  /**
   * One satisfying partial assignment, without enumerating the full space.
   * Returns undefined for the unsat node.
   */
  witness(a: BDDNode): Record<string, boolean> | undefined {
    if (a === this.FALSE) {
      return undefined;
    }
    const assignment: Record<string, boolean> = {};
    let node = a;
    while (node !== this.TRUE) {
      const internal = this.nodes[node - 2];
      assignment[this.variables[internal.variable]] = internal.high !== this.FALSE;
      node = internal.high !== this.FALSE ? internal.high : internal.low;
    }
    return assignment;
  }

  /** Compile a symbolic gate expression into a canonical BDD node. */
  compile(expr: GateExpr): BDDNode {
    switch (expr.kind) {
      case 'const':
        return expr.value ? this.TRUE : this.FALSE;
      case 'var':
        return this.literal(expr.name);
      case 'not':
        return this.not(this.compile(expr.arg));
      case 'and':
        return this.andAll(expr.args.map((arg) => this.compile(arg)));
      case 'or':
        return this.orAll(expr.args.map((arg) => this.compile(arg)));
    }
  }

  private mk(variable: number, low: BDDNode, high: BDDNode): BDDNode {
    if (low === high) {
      return low;
    }
    const key = `${variable}:${low}:${high}`;
    const cached = this.cache.get(key);
    if (cached !== undefined) {
      return cached;
    }
    const node = this.nodes.length + 2;
    this.nodes.push({ variable, low, high });
    this.cache.set(key, node);
    return node;
  }

  private ite(i: BDDNode, t: BDDNode, e: BDDNode): BDDNode {
    if (i === this.TRUE) {
      return t;
    }
    if (i === this.FALSE) {
      return e;
    }
    if (t === this.TRUE && e === this.FALSE) {
      return i;
    }
    const key = `${i}?${t}:${e}`;
    const cached = this.iteCache.get(key);
    if (cached !== undefined) {
      return cached;
    }
    const nodeI = this.nodes[i - 2];
    const top = this.topVar(i, t, e);
    const high = this.ite(this.coFactor(i, top, true), this.coFactor(t, top, true), this.coFactor(e, top, true));
    const low = this.ite(this.coFactor(i, top, false), this.coFactor(t, top, false), this.coFactor(e, top, false));
    const result = this.mk(top, low, high);
    this.iteCache.set(key, result);
    return result;
  }

  private topVar(a: BDDNode, b: BDDNode, c: BDDNode): number {
    return Math.min(this.varOf(a), this.varOf(b), this.varOf(c));
  }

  private varOf(node: BDDNode): number {
    return node < 2 ? Number.POSITIVE_INFINITY : this.nodes[node - 2].variable;
  }

  private coFactor(node: BDDNode, variable: number, value: boolean): BDDNode {
    if (node < 2) {
      return node;
    }
    const internal = this.nodes[node - 2];
    if (internal.variable === variable) {
      return value ? internal.high : internal.low;
    }
    return node;
  }
}

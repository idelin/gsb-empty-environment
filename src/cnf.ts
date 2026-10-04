// CNF builder with Tseitin-encoded gates.
// A literal is a nonzero integer: positive = variable, negative = negation.
// Variable 1 is reserved as the constant-true literal (unit clause [1]).

export type Lit = number;

export class CnfBuilder {
  nvars = 0;
  readonly clauses: number[][] = [];
  readonly trueLit: Lit;
  readonly falseLit: Lit;

  constructor() {
    this.trueLit = this.newVar();
    this.falseLit = -this.trueLit;
    this.addClause([this.trueLit]);
  }

  newVar(): Lit {
    return ++this.nvars;
  }

  addClause(lits: number[]): void {
    this.clauses.push(lits);
  }

  isConst(l: Lit): boolean {
    return l === this.trueLit || l === this.falseLit;
  }

  not(a: Lit): Lit {
    return -a;
  }

  and(a: Lit, b: Lit): Lit {
    if (a === this.falseLit || b === this.falseLit) return this.falseLit;
    if (a === this.trueLit) return b;
    if (b === this.trueLit) return a;
    if (a === b) return a;
    if (a === -b) return this.falseLit;
    const out = this.newVar();
    this.addClause([-out, a]);
    this.addClause([-out, b]);
    this.addClause([-a, -b, out]);
    return out;
  }

  or(a: Lit, b: Lit): Lit {
    if (a === this.trueLit || b === this.trueLit) return this.trueLit;
    if (a === this.falseLit) return b;
    if (b === this.falseLit) return a;
    if (a === b) return a;
    if (a === -b) return this.trueLit;
    const out = this.newVar();
    this.addClause([-a, out]);
    this.addClause([-b, out]);
    this.addClause([a, b, -out]);
    return out;
  }

  xor(a: Lit, b: Lit): Lit {
    if (a === this.falseLit) return b;
    if (b === this.falseLit) return a;
    if (a === this.trueLit) return -b;
    if (b === this.trueLit) return -a;
    if (a === b) return this.falseLit;
    if (a === -b) return this.trueLit;
    const out = this.newVar();
    this.addClause([-a, -b, -out]);
    this.addClause([a, b, -out]);
    this.addClause([-a, b, out]);
    this.addClause([a, -b, out]);
    return out;
  }

  // out = c ? a : b
  mux(c: Lit, a: Lit, b: Lit): Lit {
    if (c === this.trueLit) return a;
    if (c === this.falseLit) return b;
    if (a === b) return a;
    if (a === this.trueLit && b === this.falseLit) return c;
    if (a === this.falseLit && b === this.trueLit) return -c;
    if (a === this.falseLit) return this.and(-c, b);
    if (b === this.falseLit) return this.and(c, a);
    if (a === this.trueLit) return this.or(c, b);
    if (b === this.trueLit) return this.or(-c, a);
    const out = this.newVar();
    this.addClause([-c, -a, out]);
    this.addClause([-c, a, -out]);
    this.addClause([c, -b, out]);
    this.addClause([c, b, -out]);
    return out;
  }
}

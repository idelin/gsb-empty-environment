/**
 * A small CDCL SAT solver (conflict-driven clause learning, first-UIP
 * scheme) with two watched literals and a step budget.
 *
 * The solver gives the equivalence checker a fully self-contained symbolic
 * backend (no external SMT solver). It supports incremental clause addition
 * before solving and repeated `solve` calls with assumptions, which the
 * checker uses to minimize counterexamples bit by bit. Learnt clauses are
 * logical consequences of the permanent formula, so they remain valid across
 * `solve` calls with different assumptions.
 *
 * Budget: every unit-propagation step and every conflict consumes one unit of
 * the shared budget. When the budget is exhausted a `BudgetExhausted` error
 * is thrown; callers are expected to map that to an "unknown" verdict.
 */

export class BudgetExhausted extends Error {
  constructor() {
    super('SAT solver budget exhausted');
    this.name = 'BudgetExhausted';
  }
}

export interface Budget {
  remaining: number;
}

const NO_REASON = -1;

export class SatSolver {
  private readonly budget: Budget;
  private numVars = 0;
  // assigns[v]: 0 = unassigned, 1 = true, -1 = false (index 0 unused).
  private assigns: number[] = [0];
  // level[v]: decision level at which v was assigned.
  private level: number[] = [0];
  // reason[v]: index of the clause that implied v, or NO_REASON.
  private reason: number[] = [NO_REASON];
  private clauses: number[][] = [];
  // watches[litIndex(lit)] = indices of clauses watching `lit`.
  private watches: number[][][] = [[], []];
  private trail: number[] = [];
  private trailLim: number[] = [];
  private qhead = 0;
  private unsat = false;
  // VSIDS activity scores and restart state.
  private activity: number[] = [0];
  private varInc = 1;
  private readonly varDecay = 0.95;
  private conflicts = 0;
  private restartLimit = 100;

  constructor(budget: Budget) {
    this.budget = budget;
  }

  get variableCount(): number {
    return this.numVars;
  }

  newVar(): number {
    const v = ++this.numVars;
    this.assigns.push(0);
    this.level.push(0);
    this.reason.push(NO_REASON);
    this.activity.push(0);
    this.watches.push([], []);
    return v;
  }

  private static litIndex(lit: number): number {
    return lit > 0 ? 2 * lit : -2 * lit + 1;
  }

  private litValue(lit: number): number {
    const v = lit > 0 ? lit : -lit;
    const a = this.assigns[v];
    if (a === 0) return 0;
    return lit > 0 ? a : -a;
  }

  private decisionLevel(): number {
    return this.trailLim.length;
  }

  /** Add a top-level clause. Must only be called between solves. */
  addClause(input: readonly number[]): void {
    if (this.unsat) return;
    this.cancelUntil(0);
    const lits: number[] = [];
    const seen = new Set<number>();
    for (const lit of input) {
      const value = this.litValue(lit);
      if (value === 1) return; // already satisfied at level 0
      if (value === -1) continue; // falsified at level 0
      if (seen.has(lit)) continue;
      if (seen.has(-lit)) return; // tautology
      seen.add(lit);
      lits.push(lit);
    }
    if (lits.length === 0) {
      this.unsat = true;
      return;
    }
    if (lits.length === 1) {
      if (!this.enqueue(lits[0], NO_REASON)) this.unsat = true;
      return;
    }
    this.attachClause(lits);
  }

  /** Append a clause without simplification and attach its watches. */
  private attachClause(lits: number[]): number {
    const ci = this.clauses.length;
    this.clauses.push(lits);
    this.watches[SatSolver.litIndex(lits[0])].push(ci);
    this.watches[SatSolver.litIndex(lits[1])].push(ci);
    return ci;
  }

  private enqueue(lit: number, reason: number): boolean {
    const v = lit > 0 ? lit : -lit;
    const value = lit > 0 ? 1 : -1;
    const current = this.assigns[v];
    if (current !== 0) return current === value;
    this.assigns[v] = value;
    this.level[v] = this.decisionLevel();
    this.reason[v] = reason;
    this.trail.push(lit);
    return true;
  }

  private cancelUntil(level: number): void {
    if (this.decisionLevel() <= level) return;
    while (this.trailLim.length > level) {
      const lim = this.trailLim.pop() as number;
      while (this.trail.length > lim) {
        const lit = this.trail.pop() as number;
        this.assigns[lit > 0 ? lit : -lit] = 0;
      }
    }
    this.qhead = this.trail.length;
  }

  /** Returns the conflicting clause, or null when no conflict exists. */
  private propagate(): number[] | null {
    while (this.qhead < this.trail.length) {
      if (this.budget.remaining-- <= 0) throw new BudgetExhausted();
      const p = this.trail[this.qhead++];
      const falseLit = -p;
      const ws = this.watches[SatSolver.litIndex(falseLit)];
      let i = 0;
      let j = 0;
      while (i < ws.length) {
        const ci = ws[i++];
        const clause = this.clauses[ci];
        // Make sure the falsified watched literal sits at index 1.
        if (clause[0] === falseLit) {
          clause[0] = clause[1];
          clause[1] = falseLit;
        }
        const first = clause[0];
        if (this.litValue(first) === 1) {
          ws[j++] = ci; // clause satisfied, keep watch
          continue;
        }
        // Try to move the watch to a non-falsified literal.
        let replaced = false;
        for (let k = 2; k < clause.length; k++) {
          if (this.litValue(clause[k]) !== -1) {
            clause[1] = clause[k];
            clause[k] = falseLit;
            this.watches[SatSolver.litIndex(clause[1])].push(ci);
            replaced = true;
            break;
          }
        }
        if (replaced) continue; // watch leaves this list
        ws[j++] = ci; // clause is unit or conflicting, keep watch
        if (this.litValue(first) === -1) {
          // Conflict: finish compacting, then report the clause.
          this.qhead = this.trail.length;
          while (i < ws.length) ws[j++] = ws[i++];
          ws.length = j;
          return clause;
        }
        this.enqueue(first, ci); // unit clause: `first` is unassigned here
      }
      ws.length = j;
    }
    return null;
  }

  /**
   * First-UIP conflict analysis. Returns the learnt clause (whose first
   * literal is the asserting one) and the level to backjump to.
   */
  private analyze(confl: number[]): { learnt: number[]; backtrackLevel: number } {
    const learnt: number[] = [0]; // slot 0 reserved for the asserting literal
    const seen = new Uint8Array(this.numVars + 1);
    const currentLevel = this.decisionLevel();
    let pathC = 0;
    let backtrackLevel = 0;
    let clause = confl;
    let p = 0;
    let index = this.trail.length - 1;
    do {
      for (const q of clause) {
        const v = q > 0 ? q : -q;
        if (v === (p > 0 ? p : -p)) continue; // skip the resolved variable
        if (seen[v] === 0 && this.level[v] > 0) {
          seen[v] = 1;
          if (this.level[v] >= currentLevel) {
            pathC++;
          } else {
            learnt.push(q);
            if (this.level[v] > backtrackLevel) backtrackLevel = this.level[v];
          }
        }
      }
      // Find the most recent seen assignment on the trail; while pathC > 0 it
      // is guaranteed to sit at the current decision level.
      let lit = this.trail[index--];
      while (seen[lit > 0 ? lit : -lit] === 0) {
        lit = this.trail[index--];
      }
      p = lit;
      seen[p > 0 ? p : -p] = 0;
      pathC--;
      clause = this.clauses[this.reason[p > 0 ? p : -p]];
    } while (pathC > 0);
    learnt[0] = -p;
    for (const lit of learnt) this.bumpVar(lit > 0 ? lit : -lit);
    this.decayActivities();
    return { learnt, backtrackLevel };
  }

  private bumpVar(v: number): void {
    this.activity[v] += this.varInc;
    if (this.activity[v] > 1e100) {
      for (let i = 1; i <= this.numVars; i++) this.activity[i] *= 1e-100;
      this.varInc *= 1e-100;
    }
  }

  private decayActivities(): void {
    this.varInc /= this.varDecay;
  }

  private pickBranchVar(): number {
    let best = 0;
    let bestActivity = -1;
    for (let v = 1; v <= this.numVars; v++) {
      if (this.assigns[v] === 0 && this.activity[v] > bestActivity) {
        best = v;
        bestActivity = this.activity[v];
      }
    }
    return best;
  }

  /**
   * CDCL main loop. Assumptions (if any) are already enqueued, one per
   * decision level, occupying levels 1..assumptionLevels. Backjumps are
   * clamped to the assumption levels so assumptions stay on the trail; a
   * conflict at or below them means unsatisfiable under these assumptions.
   * Learnt clauses remain valid across solves: they are logical consequences
   * of the permanent formula (assumption literals may appear inside them).
   */
  private searchLoop(assumptionLevels: number): boolean {
    for (;;) {
      const confl = this.propagate();
      if (confl !== null) {
        if (this.budget.remaining-- <= 0) throw new BudgetExhausted();
        if (this.decisionLevel() <= assumptionLevels) return false;
        const { learnt, backtrackLevel } = this.analyze(confl);
        this.cancelUntil(Math.max(backtrackLevel, assumptionLevels));
        if (learnt.length === 1) {
          if (!this.enqueue(learnt[0], NO_REASON)) return false;
        } else {
          const ci = this.attachClause(learnt);
          this.enqueue(learnt[0], ci);
        }
        this.conflicts++;
        if (this.conflicts >= this.restartLimit) {
          // Geometric restart schedule; learnt clauses survive restarts.
          this.restartLimit = Math.floor(this.restartLimit * 1.5);
          this.cancelUntil(assumptionLevels);
        }
        continue;
      }
      const v = this.pickBranchVar();
      if (v === 0) return true;
      this.trailLim.push(this.trail.length);
      this.enqueue(v, NO_REASON);
    }
  }

  /**
   * Solve under the given assumptions (temporary, only for this call).
   * Returns true when satisfiable; the model is then available via `valueOf`.
   */
  solve(assumptions: readonly number[] = []): boolean {
    if (this.unsat) return false;
    this.cancelUntil(0);
    // Each assumption gets its own decision level (required by first-UIP
    // analysis, which expects exactly one reason-less literal per level).
    for (const lit of assumptions) {
      this.trailLim.push(this.trail.length);
      if (!this.enqueue(lit, NO_REASON)) {
        this.cancelUntil(0);
        return false;
      }
      if (this.propagate() !== null) {
        this.cancelUntil(0);
        return false;
      }
    }
    const sat = this.searchLoop(assumptions.length);
    if (!sat) this.cancelUntil(0);
    return sat;
  }

  /** Value of a variable in the current model (call after a SAT `solve`). */
  valueOf(variable: number): boolean {
    return this.assigns[variable] === 1;
  }
}

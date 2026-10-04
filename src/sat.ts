// CDCL SAT solver: unit propagation via occurrence lists, 1-UIP conflict
// analysis with clause learning, non-chronological backtracking, assumption
// support, and a resource budget. When the budget is exhausted the solver
// reports 'unknown' instead of an answer.

export type SatStatus = 'sat' | 'unsat' | 'unknown';

export interface SatBudget {
  maxConflicts: number;
  maxPropagations: number;
}

export const DEFAULT_BUDGET: SatBudget = {
  maxConflicts: 100_000,
  maxPropagations: 20_000_000,
};

export class SatSolver {
  readonly nvars: number;
  private readonly clauses: number[][];
  private readonly occurs: number[][];
  private readonly assigns: Int8Array;
  private readonly level: Int32Array;
  private readonly reason: Int32Array;
  private readonly seen: Uint8Array;
  private readonly marked: number[] = [];
  private readonly activity: Float64Array;
  private readonly phase: Int8Array;
  private varInc = 1;
  private readonly varDecay = 0.95;
  private restartLimit = 100;
  private restartCount = 0;
  private readonly trail: number[] = [];
  private readonly trailLim: number[] = [];
  private qhead = 0;
  private prepared = false;
  private conflicts = 0;
  private propagations = 0;
  private budget: SatBudget;
  private assumptionBase = 0;
  lastModel: Int8Array | null = null;

  constructor(nvars: number, clauses: number[][], budget: Partial<SatBudget> = {}) {
    this.nvars = nvars;
    this.clauses = clauses.map((c) => c.slice());
    this.budget = { ...DEFAULT_BUDGET, ...budget };
    this.assigns = new Int8Array(nvars + 1);
    this.level = new Int32Array(nvars + 1);
    this.reason = new Int32Array(nvars + 1).fill(-1);
    this.seen = new Uint8Array(nvars + 1);
    this.activity = new Float64Array(nvars + 1);
    this.phase = new Int8Array(nvars + 1).fill(1);
    this.occurs = Array.from({ length: 2 * nvars + 1 }, () => []);
    for (let i = 0; i < this.clauses.length; i++) this.watchClause(i);
  }

  private watchClause(ci: number): void {
    for (const lit of this.clauses[ci]) this.occurs[lit + this.nvars].push(ci);
  }

  private valueOf(lit: number): number {
    const v = this.assigns[Math.abs(lit)];
    return lit > 0 ? v : -v;
  }

  private enqueue(lit: number, reason: number): boolean {
    const v = Math.abs(lit);
    const cur = this.assigns[v];
    if (cur !== 0) return cur === (lit > 0 ? 1 : -1);
    this.assigns[v] = lit > 0 ? 1 : -1;
    this.level[v] = this.trailLim.length;
    this.reason[v] = reason;
    this.trail.push(lit);
    return true;
  }

  // Returns a conflicting clause index, -1 on budget exhaustion, or null.
  private propagate(): number | null {
    while (this.qhead < this.trail.length) {
      const lit = this.trail[this.qhead++];
      if (++this.propagations > this.budget.maxPropagations) return -1;
      for (const ci of this.occurs[-lit + this.nvars]) {
        const clause = this.clauses[ci];
        let unassigned = 0;
        let lastUnassigned = 0;
        let satisfied = false;
        for (const l of clause) {
          const val = this.valueOf(l);
          if (val > 0) { satisfied = true; break; }
          if (val === 0) { unassigned++; lastUnassigned = l; }
        }
        if (satisfied) continue;
        if (unassigned === 0) return ci;
        if (unassigned === 1 && !this.enqueue(lastUnassigned, ci)) return ci;
      }
    }
    return null;
  }

  private cancelTo(level: number): void {
    const target = level < this.trailLim.length ? this.trailLim[level] : this.trail.length;
    for (let i = this.trail.length - 1; i >= target; i--) {
      const v = Math.abs(this.trail[i]);
      this.phase[v] = this.assigns[v];
      this.assigns[v] = 0;
    }
    this.trail.length = target;
    this.trailLim.length = level;
    if (this.qhead > target) this.qhead = target;
  }

  private pickBranchVar(): number {
    let best = 0;
    let bestAct = -1;
    for (let v = 1; v <= this.nvars; v++) {
      if (this.assigns[v] === 0 && this.activity[v] > bestAct) {
        bestAct = this.activity[v];
        best = v;
      }
    }
    return best;
  }

  private bumpVar(v: number): void {
    this.activity[v] += this.varInc;
    if (this.activity[v] > 1e100) {
      for (let i = 1; i <= this.nvars; i++) this.activity[i] *= 1e-100;
      this.varInc *= 1e-100;
    }
  }

  // 1-UIP conflict analysis. Returns the learnt clause (asserting literal
  // last) and the backtrack level.
  private analyze(conflIdx: number): { learnt: number[]; backtrack: number } {
    const learnt: number[] = [];
    const curLevel = this.trailLim.length;
    let pathC = 0;
    let idx = this.trail.length - 1;
    let p = 0;
    let clause = this.clauses[conflIdx];
    try {
      for (;;) {
        for (const q of clause) {
          const v = Math.abs(q);
          if (v === Math.abs(p) || this.seen[v] !== 0 || this.level[v] === 0) continue;
          this.seen[v] = 1;
          this.marked.push(v);
          if (this.level[v] === curLevel) pathC++;
          else learnt.push(q);
        }
        do {
          p = this.trail[idx--];
        } while (this.seen[Math.abs(p)] === 0);
        pathC--;
        if (pathC === 0) break;
        clause = this.clauses[this.reason[Math.abs(p)]];
      }
      learnt.push(-p);
      let backtrack = 0;
      for (let i = 0; i < learnt.length - 1; i++) {
        const l = this.level[Math.abs(learnt[i])];
        if (l > backtrack) backtrack = l;
      }
      for (const v of this.marked) this.bumpVar(v);
      this.varInc /= this.varDecay;
      return { learnt, backtrack };
    } finally {
      for (const v of this.marked) this.seen[v] = 0;
      this.marked.length = 0;
    }
  }

  // Solves under the given assumptions. Restores decision level 0 before
  // returning; on 'sat' the satisfying assignment is in `lastModel`.
  solve(assumptions: number[] = []): SatStatus {
    this.lastModel = null;
    if (!this.prepared) {
      this.prepared = true;
      for (let i = 0; i < this.clauses.length; i++) {
        const clause = this.clauses[i];
        if (clause.length === 0) return 'unsat';
        if (clause.length === 1 && !this.enqueue(clause[0], i)) return 'unsat';
      }
      const confl = this.propagate();
      if (confl !== null) return confl === -1 ? 'unknown' : 'unsat';
    }
    for (const a of assumptions) {
      this.trailLim.push(this.trail.length);
      if (!this.enqueue(a, -1)) {
        this.cancelTo(0);
        return 'unsat';
      }
    }
    this.assumptionBase = this.trailLim.length;
    const status = this.mainLoop();
    if (status === 'sat') this.lastModel = this.assigns.slice();
    this.cancelTo(0);
    return status;
  }

  private mainLoop(): SatStatus {
    for (;;) {
      const confl = this.propagate();
      if (confl === -1) return 'unknown';
      if (confl !== null) {
        if (this.trailLim.length === this.assumptionBase) return 'unsat';
        this.conflicts++;
        if (this.conflicts > this.budget.maxConflicts) return 'unknown';
        if (++this.restartCount >= this.restartLimit) {
          // Luby-style geometric restart schedule; learnt clauses persist.
          this.restartCount = 0;
          this.restartLimit = Math.floor(this.restartLimit * 1.5) + 1;
          this.cancelTo(this.assumptionBase);
          continue;
        }
        const { learnt, backtrack } = this.analyze(confl);
        this.cancelTo(Math.max(backtrack, this.assumptionBase));
        const ci = this.clauses.length;
        this.clauses.push(learnt);
        this.watchClause(ci);
        this.enqueue(learnt[learnt.length - 1], ci);
        continue;
      }
      const v = this.pickBranchVar();
      if (v === 0) return 'sat';
      this.trailLim.push(this.trail.length);
      this.enqueue(this.phase[v] >= 0 ? v : -v, -1);
    }
  }

  modelValue(lit: number): boolean {
    if (!this.lastModel) throw new Error('no model available');
    const v = this.lastModel[Math.abs(lit)];
    return lit > 0 ? v === 1 : v === -1;
  }
}

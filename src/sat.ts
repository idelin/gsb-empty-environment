/**
 * A compact CDCL SAT solver (MiniSat-style) with assumption support:
 * watched literals, first-UIP clause learning, VSIDS-style variable
 * activity, and phase saving. Clauses and variables may be added
 * incrementally between solve() calls.
 *
 * Literals are non-zero integers; a positive literal denotes its variable.
 */
export class SatSolver {
  private clauses: number[][] = [];
  /** watch lists, indexed by literal index (see litIdx). */
  private watches: number[][] = [[], []]; // index 0/1 unused (no variable 0)
  private assigns: number[] = [0]; // -1 | 0 | 1, by variable (1-based)
  private level: number[] = [0]; // decision level per variable
  private reason: number[] = [-1]; // clause index that implied the var, or -1
  private trail: number[] = [];
  private trailLim: number[] = [];
  private qhead = 0;
  private activity: number[] = [0];
  private varInc = 1;
  private readonly varDecay = 0.95;
  private polarity: number[] = [0]; // saved phase per variable
  private numVars = 0;
  private inconsistent = false; // an empty clause was added at level 0

  private static litIdx(lit: number): number {
    return lit > 0 ? lit * 2 : -lit * 2 + 1;
  }

  newVar(): number {
    this.numVars += 1;
    this.assigns.push(0);
    this.level.push(0);
    this.reason.push(-1);
    this.activity.push(0);
    this.polarity.push(-1); // default: try false first
    this.watches.push([]);
    this.watches.push([]);
    return this.numVars;
  }

  get varCount(): number {
    return this.numVars;
  }

  addClause(lits: number[]): void {
    // Must be called at decision level 0 (solve() always cancels to 0).
    const seen = new Set<number>();
    const clean: number[] = [];
    for (const l of lits) {
      const v = Math.abs(l);
      const val = this.assigns[v];
      if (val !== 0 && (val === 1) === l > 0) return; // already satisfied at level 0
      if (seen.has(-l)) return; // tautology
      if (val === 0 && !seen.has(l)) {
        seen.add(l);
        clean.push(l);
      }
    }
    const ci = this.clauses.length;
    this.clauses.push(clean);
    if (clean.length === 0) {
      this.inconsistent = true; // empty clause: formula is UNSAT
      return;
    }
    if (clean.length === 1) {
      this.watches[SatSolver.litIdx(clean[0])].push(ci);
      if (!this.enqueue(clean[0], ci)) {
        this.inconsistent = true;
        return;
      }
      // Keep level 0 fully propagated so conflicts at level 0 are
      // detected here rather than inside conflict analysis.
      if (this.propagate() !== -1) this.inconsistent = true;
      return;
    }
    this.watches[SatSolver.litIdx(clean[0])].push(ci);
    this.watches[SatSolver.litIdx(clean[1])].push(ci);
  }

  private value(lit: number): number {
    const a = this.assigns[Math.abs(lit)];
    if (a === 0) return 0;
    return (a === 1) === lit > 0 ? 1 : -1;
  }

  private enqueue(lit: number, from: number): boolean {
    const v = Math.abs(lit);
    const val = lit > 0 ? 1 : -1;
    if (this.assigns[v] !== 0) return this.assigns[v] === val;
    this.assigns[v] = val;
    this.level[v] = this.trailLim.length;
    this.reason[v] = from;
    this.trail.push(lit);
    return true;
  }

  private propagate(): number {
    while (this.qhead < this.trail.length) {
      const p = this.trail[this.qhead++];
      const watchList = this.watches[SatSolver.litIdx(-p)];
      let i = 0;
      let out = 0;
      while (i < watchList.length) {
        const ci = watchList[i++];
        const c = this.clauses[ci];
        // Make sure c[1] is the falsified watched literal.
        if (c[0] === -p) {
          c[0] = c[1];
          c[1] = -p;
        }
        if (this.value(c[0]) === 1) {
          watchList[out++] = ci; // satisfied by the other watch
          continue;
        }
        // Look for a replacement watch.
        let replaced = false;
        for (let k = 2; k < c.length; k++) {
          if (this.value(c[k]) !== -1) {
            const tmp = c[1];
            c[1] = c[k];
            c[k] = tmp;
            this.watches[SatSolver.litIdx(c[1])].push(ci);
            replaced = true;
            break;
          }
        }
        if (replaced) continue;
        watchList[out++] = ci;
        const first = this.value(c[0]);
        if (first === -1) {
          // Conflict. Keep the remaining watches untouched.
          while (i < watchList.length) watchList[out++] = watchList[i++];
          watchList.length = out;
          return ci;
        }
        this.enqueue(c[0], ci);
      }
      watchList.length = out;
    }
    return -1;
  }

  private bumpVar(v: number): void {
    this.activity[v] += this.varInc;
    if (this.activity[v] > 1e100) {
      for (let i = 1; i <= this.numVars; i++) this.activity[i] *= 1e-100;
      this.varInc *= 1e-100;
    }
  }

  /** First-UIP conflict analysis. Returns [learntClause, backtrackLevel]. */
  private analyze(confl: number): [number[], number] {
    const seen = new Array<boolean>(this.numVars + 1).fill(false);
    const learnt: number[] = [0]; // placeholder for the asserting literal
    let backtrackLevel = 0;
    let pathC = 0;
    let ci: number = confl;
    let idx = this.trail.length;
    let p = 0;
    do {
      const c = this.clauses[ci];
      for (let k = p === 0 ? 0 : 1; k < c.length; k++) {
        const v = Math.abs(c[k]);
        if (!seen[v] && this.level[v] > 0) {
          seen[v] = true;
          this.bumpVar(v);
          if (this.level[v] >= this.trailLim.length) {
            pathC++;
          } else {
            learnt.push(c[k]);
            if (this.level[v] > backtrackLevel) backtrackLevel = this.level[v];
          }
        }
      }
      do {
        idx--;
        p = this.trail[idx];
      } while (!seen[Math.abs(p)]);
      seen[Math.abs(p)] = false;
      pathC--;
      ci = this.reason[Math.abs(p)];
    } while (pathC > 0);
    learnt[0] = -p;
    return [learnt, backtrackLevel];
  }

  private cancelTo(target: number): void {
    if (this.trailLim.length <= target) return;
    for (let i = this.trail.length - 1; i >= this.trailLim[target]; i--) {
      const v = Math.abs(this.trail[i]);
      this.polarity[v] = this.assigns[v];
      this.assigns[v] = 0;
      this.reason[v] = -1;
    }
    this.trail.length = this.trailLim[target];
    this.trailLim.length = target;
    this.qhead = this.trail.length;
  }

  private pickBranchVar(): number {
    let best = 0;
    for (let v = 1; v <= this.numVars; v++) {
      if (this.assigns[v] === 0 && (best === 0 || this.activity[v] > this.activity[best])) {
        best = v;
      }
    }
    return best;
  }

  /**
   * Solve under the given assumption literals.
   * Returns a full model (1-based array, index 0 unused) or null if UNSAT.
   */
  solve(assumptions: number[] = []): boolean[] | null {
    if (this.inconsistent) return null;
    let assumptionLevels = 0;
    for (const a of assumptions) {
      const v = Math.abs(a);
      const val = a > 0 ? 1 : -1;
      if (this.assigns[v] !== 0) {
        if (this.assigns[v] !== val) {
          this.cancelTo(0);
          return null;
        }
        continue;
      }
      this.trailLim.push(this.trail.length);
      assumptionLevels++;
      this.enqueue(a, -1);
      // Propagate immediately: a conflict here means the assumptions are
      // inconsistent with the formula on their own.
      if (this.propagate() !== -1) {
        this.cancelTo(0);
        return null;
      }
    }

    let result: boolean[] | null = null;
    for (;;) {
      const confl = this.propagate();
      if (confl !== -1) {
        // A conflict at or below the assumption levels is a real
        // contradiction: assumptions are never unassigned during search.
        if (this.trailLim.length <= assumptionLevels) break; // UNSAT
        const [learnt, btLevel] = this.analyze(confl);
        this.cancelTo(Math.max(btLevel, assumptionLevels));
        const ci = this.clauses.length;
        this.clauses.push(learnt);
        if (learnt.length === 1) {
          this.watches[SatSolver.litIdx(learnt[0])].push(ci);
        } else {
          this.watches[SatSolver.litIdx(learnt[0])].push(ci);
          this.watches[SatSolver.litIdx(learnt[1])].push(ci);
        }
        this.enqueue(learnt[0], ci);
        this.varInc /= this.varDecay;
      } else {
        const next = this.pickBranchVar();
        if (next === 0) {
          result = new Array(this.numVars + 1).fill(false);
          for (let v = 1; v <= this.numVars; v++) result[v] = this.assigns[v] === 1;
          break;
        }
        this.trailLim.push(this.trail.length);
        const pol = this.polarity[next];
        this.enqueue(pol === 1 ? next : -next, -1);
      }
    }
    this.cancelTo(0);
    return result;
  }
}

/**
 * Encode "at most k of xs is true" (Sinz sequential counter), guarded by an
 * activation literal: the constraint only applies while `act` is assumed true.
 */
export function addAtMost(sat: SatSolver, xs: number[], k: number, act: number): void {
  const guard = -act;
  const n = xs.length;
  if (k >= n) return;
  if (k === 0) {
    for (const x of xs) sat.addClause([-x, guard]);
    return;
  }
  // s[i][j]: among xs[0..i], at least j+1 are true (i < n-1, j < k)
  const s: number[][] = [];
  for (let i = 0; i < n - 1; i++) {
    const row: number[] = [];
    for (let j = 0; j < k; j++) row.push(sat.newVar());
    s.push(row);
  }
  sat.addClause([-xs[0], s[0][0], guard]);
  for (let j = 1; j < k; j++) sat.addClause([-s[0][j], guard]);
  for (let i = 1; i < n - 1; i++) {
    sat.addClause([-xs[i], s[i][0], guard]);
    sat.addClause([-s[i - 1][0], s[i][0], guard]);
    for (let j = 1; j < k; j++) {
      sat.addClause([-xs[i], -s[i - 1][j - 1], s[i][j], guard]);
      sat.addClause([-s[i - 1][j], s[i][j], guard]);
    }
    // Overflow: xs[i] may not be set once s[i-1] already counts k trues.
    sat.addClause([-xs[i], -s[i - 1][k - 1], guard]);
  }
  sat.addClause([-xs[n - 1], -s[n - 2][k - 1], guard]);
}

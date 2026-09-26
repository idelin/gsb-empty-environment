/**
 * A small boolean constraint solver over cardinality constraints.
 *
 * Variables are tri-state: -1 unassigned, 0 absent, 1 present.
 * Constraints are cardinality bounds `min <= sum(vars) <= max`, which
 * uniformly encode exactlyOne (1..1), atLeastOne (1..n) and atMostK (0..k).
 *
 * The solver supports scoped local modification: `push()` marks the trail,
 * `pop()` undoes every assignment / constraint / variable added since the
 * matching push, fully restoring the propagation state. This is what the
 * joint-satisfiability and minimal-core algorithms use to try hypotheses
 * without rebuilding the problem.
 *
 * Propagation alone is NOT complete for overlapping cardinality
 * constraints, so `check()` falls back to small-domain enumeration and
 * verifies every candidate witness against all constraints.
 */

export interface CardConstraint {
  id: string;
  vars: number[];
  min: number;
  max: number;
}

export type SolverValue = -1 | 0 | 1;

type TrailEntry =
  | { kind: 'var' }
  | { kind: 'constraint' }
  | { kind: 'assign'; v: number; prev: SolverValue };

export interface CheckResult {
  sat: boolean;
  /** Present iff sat: one value per variable, in creation order. */
  witness?: boolean[];
}

export class Solver {
  private values: SolverValue[] = [];
  private constraints: CardConstraint[] = [];
  private trail: TrailEntry[] = [];
  private marks: number[] = [];

  get varCount(): number {
    return this.values.length;
  }

  get depth(): number {
    return this.marks.length;
  }

  addVar(): number {
    this.values.push(-1);
    this.trail.push({ kind: 'var' });
    return this.values.length - 1;
  }

  addConstraint(constraint: CardConstraint): void {
    this.constraints.push(constraint);
    this.trail.push({ kind: 'constraint' });
  }

  value(v: number): SolverValue {
    return this.values[v];
  }

  /** Returns false (without changing state) iff the assignment contradicts an existing one. */
  assign(v: number, val: SolverValue): boolean {
    const prev = this.values[v];
    if (prev !== -1) return prev === val;
    this.values[v] = val;
    this.trail.push({ kind: 'assign', v, prev });
    return true;
  }

  push(): void {
    this.marks.push(this.trail.length);
  }

  pop(): void {
    const mark = this.marks.pop();
    if (mark === undefined) throw new Error('Solver.pop() without matching push()');
    while (this.trail.length > mark) {
      const entry = this.trail.pop() as TrailEntry;
      if (entry.kind === 'assign') this.values[entry.v] = entry.prev;
      else if (entry.kind === 'constraint') this.constraints.pop();
      else this.values.pop();
    }
  }

  /**
   * Bounds-consistency propagation for cardinality constraints, to fixpoint.
   * Returns false on a detected conflict. All forced assignments are trailed.
   */
  propagate(): boolean {
    let changed = true;
    while (changed) {
      changed = false;
      for (const c of this.constraints) {
        let trues = 0;
        const unassigned: number[] = [];
        for (const v of c.vars) {
          const val = this.values[v];
          if (val === 1) trues++;
          else if (val === -1) unassigned.push(v);
        }
        if (trues > c.max) return false;
        if (trues + unassigned.length < c.min) return false;
        if (unassigned.length === 0) continue;
        if (trues === c.max) {
          for (const v of unassigned) if (!this.assign(v, 0)) return false;
          changed = true;
        } else if (trues + unassigned.length === c.min) {
          for (const v of unassigned) if (!this.assign(v, 1)) return false;
          changed = true;
        }
      }
    }
    return true;
  }

  private satisfied(): boolean {
    for (const c of this.constraints) {
      let trues = 0;
      for (const v of c.vars) if (this.values[v] === 1) trues++;
      if (trues < c.min || trues > c.max) return false;
    }
    return true;
  }

  /**
   * Side-effect-free satisfiability query: propagates, then enumerates the
   * remaining unassigned variables (small domain) and verifies each
   * candidate witness against every constraint. State is restored via
   * push/pop before returning.
   */
  check(): CheckResult {
    this.push();
    try {
      if (!this.propagate()) return { sat: false };
      const free: number[] = [];
      for (let i = 0; i < this.values.length; i++) {
        if (this.values[i] === -1) free.push(i);
      }
      if (free.length > 30) {
        throw new Error(`solver domain too large (${free.length} free variables)`);
      }
      const total = 2 ** free.length;
      for (let mask = 0; mask < total; mask++) {
        this.push();
        for (let bit = 0; bit < free.length; bit++) {
          this.assign(free[bit], ((mask >> bit) & 1) as SolverValue);
        }
        const ok = this.satisfied();
        const witness = ok ? this.values.map((v) => v === 1) : undefined;
        this.pop();
        if (ok) return { sat: true, witness };
      }
      return { sat: false };
    } finally {
      this.pop();
    }
  }
}

/**
 * Simple Temporal Problem (STP) solver.
 *
 * Variables are time points; constraints are difference bounds of the form
 *   value(to) - value(from) <= weight
 * which is an edge `from -> to` in the distance graph. All-pairs shortest
 * paths (Floyd-Warshall) yield the tightest implied bounds; a negative cycle
 * means the constraint set is inconsistent.
 *
 * Variable 0 is the reference time point fixed at 0 (project start), so for
 * every variable v:  -dist[v][0] <= value(v) <= dist[0][v].
 */

export interface StpSolution {
  consistent: boolean;
  /** Earliest feasible value per variable (lower bound). */
  earliest: number[];
  /** Latest feasible value per variable (upper bound). */
  latest: number[];
}

export class SimpleTemporalProblem {
  readonly reference = 0;
  private variableCount = 1;
  private edges: Array<[from: number, to: number, weight: number]> = [];

  addVariable(): number {
    return this.variableCount++;
  }

  /** Assert value(to) - value(from) <= weight. */
  addConstraint(from: number, to: number, weight: number): void {
    this.edges.push([from, to, weight]);
  }

  /** Assert lower <= value(v) <= upper (relative to the reference point). */
  addBounds(v: number, lower: number, upper: number): void {
    this.addConstraint(this.reference, v, upper);
    this.addConstraint(v, this.reference, -lower);
  }

  solve(): StpSolution {
    const n = this.variableCount;
    const dist: number[][] = Array.from({ length: n }, (_, i) =>
      Array.from({ length: n }, (_, j) => (i === j ? 0 : Infinity)),
    );
    for (const [from, to, weight] of this.edges) {
      if (weight < dist[from][to]) dist[from][to] = weight;
    }
    for (let k = 0; k < n; k++) {
      for (let i = 0; i < n; i++) {
        if (dist[i][k] === Infinity) continue;
        for (let j = 0; j < n; j++) {
          const through = dist[i][k] + dist[k][j];
          if (through < dist[i][j]) dist[i][j] = through;
        }
      }
    }
    for (let i = 0; i < n; i++) {
      if (dist[i][i] < 0) return { consistent: false, earliest: [], latest: [] };
    }
    const earliest = new Array<number>(n);
    const latest = new Array<number>(n);
    for (let i = 0; i < n; i++) {
      earliest[i] = -dist[i][this.reference] + 0; // "+ 0" normalizes -0
      latest[i] = dist[this.reference][i] + 0;
    }
    return { consistent: true, earliest, latest };
  }
}

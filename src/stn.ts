/**
 * Simple Temporal Network (STN) over time points.
 *
 * Constraints have the form  x - y <= c.  Node 0 is the reference "time zero"
 * fixed at 0.  All-pairs shortest paths (Floyd-Warshall) yield the tightest
 * implied bounds: latest(x) = dist[0][x] and earliest(x) = -dist[x][0].
 * A negative diagonal entry after closure signals inconsistency.
 */

const INF = Number.POSITIVE_INFINITY;

export class SimpleTemporalNetwork {
  readonly size: number;
  private readonly dist: number[][];

  constructor(size: number) {
    this.size = size;
    this.dist = [];
    for (let i = 0; i < size; i++) {
      this.dist.push(new Array<number>(size).fill(INF));
      this.dist[i][i] = 0;
    }
  }

  /** Add the constraint  x - y <= c. */
  addConstraint(x: number, y: number, c: number): void {
    if (c < this.dist[y][x]) this.dist[y][x] = c;
  }

  /** Compute the transitive closure; returns false on negative cycles. */
  close(): boolean {
    const d = this.dist;
    for (let k = 0; k < this.size; k++) {
      for (let i = 0; i < this.size; i++) {
        if (d[i][k] === INF) continue;
        for (let j = 0; j < this.size; j++) {
          if (d[k][j] === INF) continue;
          const via = d[i][k] + d[k][j];
          if (via < d[i][j]) d[i][j] = via;
        }
      }
    }
    for (let i = 0; i < this.size; i++) {
      if (d[i][i] < 0) return false;
    }
    return true;
  }

  /** Tightest upper bound on (x - y); Infinity if unconstrained. */
  bound(x: number, y: number): number {
    return this.dist[y][x];
  }

  /** Earliest feasible value of x relative to time zero. */
  earliest(x: number): number {
    const lb = -this.dist[x][0];
    return lb === 0 ? 0 : lb; // normalise -0
  }

  /** Latest feasible value of x relative to time zero. */
  latest(x: number): number {
    return this.dist[0][x];
  }
}

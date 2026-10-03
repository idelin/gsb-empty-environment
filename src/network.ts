/**
 * Qualitative constraint network over Allen's interval algebra.
 *
 * Nodes are time intervals; the edge C[i][j] holds the disjunction of basic
 * relations still allowed between interval i and interval j. Consistency is
 * enforced with Allen's path-consistency propagation (a transitive-closure
 * style fixpoint): whenever an edge is tightened, every triangle through that
 * edge is re-revised via relational composition and intersection.
 */

import {
  EMPTY,
  UNIVERSAL,
  composeSets,
  converseSet,
  singleton,
  type RelationSet,
} from './allen.ts';

export class AllenNetwork {
  readonly size: number;
  private readonly cells: RelationSet[][];

  constructor(size: number) {
    this.size = size;
    this.cells = [];
    for (let i = 0; i < size; i++) {
      const row: RelationSet[] = [];
      for (let j = 0; j < size; j++) {
        row.push(i === j ? singleton('eq') : UNIVERSAL);
      }
      this.cells.push(row);
    }
  }

  /** The relation set currently allowed between intervals i and j. */
  get(i: number, j: number): RelationSet {
    return this.cells[i][j];
  }

  /**
   * Restrict the relation between i and j to `set` (intersected with the
   * current value, mirrored on the converse edge) and propagate to a fixpoint.
   * Returns false if the network became inconsistent.
   */
  constrain(i: number, j: number, set: RelationSet): boolean {
    if (!this.revise(i, j, set)) return false;
    return this.propagate([[i, j]]);
  }

  /** Full path-consistency pass over the whole network. */
  propagateAll(): boolean {
    const queue: [number, number][] = [];
    for (let i = 0; i < this.size; i++) {
      for (let j = 0; j < this.size; j++) {
        if (i !== j) queue.push([i, j]);
      }
    }
    return this.propagate(queue);
  }

  private revise(i: number, j: number, allowed: RelationSet): boolean {
    const next = this.cells[i][j] & allowed;
    if (next === EMPTY) return false;
    if (next === this.cells[i][j]) return true;
    this.cells[i][j] = next;
    this.cells[j][i] = converseSet(next);
    return true;
  }

  /**
   * Allen's propagation: for each queued edge (i, j) and every third node k,
   * tighten C[i][k] with C[i][j] ∘ C[j][k] and C[k][j] with C[k][i] ∘ C[i][j].
   */
  private propagate(queue: [number, number][]): boolean {
    const queued = new Set<number>(queue.map(([i, j]) => i * this.size + j));
    const push = (i: number, j: number) => {
      const key = i * this.size + j;
      if (!queued.has(key)) {
        queued.add(key);
        queue.push([i, j]);
      }
    };
    while (queue.length > 0) {
      const [i, j] = queue.shift()!;
      queued.delete(i * this.size + j);
      for (let k = 0; k < this.size; k++) {
        if (k === i || k === j) continue;
        const viaIK = composeSets(this.cells[i][j], this.cells[j][k]);
        const before1 = this.cells[i][k];
        if (!this.revise(i, k, viaIK)) return false;
        if (this.cells[i][k] !== before1) push(i, k);
        const viaKJ = composeSets(this.cells[k][i], this.cells[i][j]);
        const before2 = this.cells[k][j];
        if (!this.revise(k, j, viaKJ)) return false;
        if (this.cells[k][j] !== before2) push(k, j);
      }
    }
    return true;
  }
}

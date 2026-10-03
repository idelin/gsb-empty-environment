/**
 * Qualitative temporal constraint network over Allen's interval algebra.
 *
 * Nodes are tasks (intervals); the edge label cells[i][j] is the 13-bit mask
 * of basic relations still allowed between task i and task j. Consistency is
 * enforced with the PC-2 path-consistency algorithm, which computes the
 * transitive closure of the constraints: every edge is tightened by
 * composing the labels along every length-2 path through a third node.
 */

import { EQ_BIT, UNIVERSAL, composeMasks, converseMask } from './allen.ts';

export class TemporalNetwork {
  readonly size: number;
  private cells: number[][];
  /** False once any edge label became empty (inconsistent network). */
  consistent = true;

  constructor(size: number) {
    this.size = size;
    this.cells = Array.from({ length: size }, (_, i) =>
      Array.from({ length: size }, (_, j) => (i === j ? EQ_BIT : UNIVERSAL)),
    );
  }

  /** Current label (mask of allowed basic relations) between i and j. */
  mask(i: number, j: number): number {
    return this.cells[i][j];
  }

  /** Intersect the edge (i, j) with the given mask (converse kept in sync). */
  intersect(i: number, j: number, mask: number): void {
    const tightened = this.cells[i][j] & mask;
    if (tightened === this.cells[i][j]) return;
    this.cells[i][j] = tightened;
    this.cells[j][i] = converseMask(tightened);
    if (tightened === 0) this.consistent = false;
  }

  /**
   * PC-2 path consistency. Returns false if the network is inconsistent.
   * After a successful run, mask(i, j) contains exactly the basic relations
   * that survive composition along every 2-path — the transitive closure of
   * the asserted constraints.
   */
  propagate(): boolean {
    const queue: Array<[number, number]> = [];
    for (let i = 0; i < this.size; i++) {
      for (let j = 0; j < this.size; j++) {
        if (i !== j) queue.push([i, j]);
      }
    }
    let head = 0;
    while (head < queue.length && this.consistent) {
      const [i, j] = queue[head++];
      for (let k = 0; k < this.size; k++) {
        if (k === i || k === j) continue;
        // Tighten (i, k) by the path i -> j -> k.
        if (this.revise(i, k, composeMasks(this.cells[i][j], this.cells[j][k]))) {
          queue.push([i, k]);
        }
        // Tighten (k, j) by the path k -> i -> j.
        if (this.revise(k, j, composeMasks(this.cells[k][i], this.cells[i][j]))) {
          queue.push([k, j]);
        }
      }
    }
    return this.consistent;
  }

  private revise(i: number, j: number, allowed: number): boolean {
    const tightened = this.cells[i][j] & allowed;
    if (tightened === this.cells[i][j]) return false;
    this.cells[i][j] = tightened;
    this.cells[j][i] = converseMask(tightened);
    if (tightened === 0) this.consistent = false;
    return true;
  }
}

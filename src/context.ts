import { TRANS_IDX_LPS, approxLpsProbability } from "./tables.ts";

export type Bin = 0 | 1;

/**
 * A single adaptive binary context: probability state index + MPS value.
 *
 * The state index tracks how skewed the distribution is; `valMPS` records
 * which symbol is currently the Most Probable Symbol. Observations move the
 * state up (MPS) or down along TRANS_IDX_LPS (LPS); an LPS at state 0 swaps
 * the MPS role, letting the model track changing statistics.
 */
export class ContextModel {
  stateIdx: number;
  valMPS: Bin;
  /** Frozen contexts never adapt (used by the static baseline model). */
  readonly frozen: boolean;

  constructor(stateIdx = 0, valMPS: Bin = 0, frozen = false) {
    this.stateIdx = stateIdx;
    this.valMPS = valMPS;
    this.frozen = frozen;
  }

  updateMPS(): void {
    if (this.frozen) return;
    if (this.stateIdx < 62) this.stateIdx++;
  }

  updateLPS(): void {
    if (this.frozen) return;
    this.stateIdx = TRANS_IDX_LPS[this.stateIdx];
    if (this.stateIdx === 0) this.valMPS = (1 - this.valMPS) as Bin;
  }

  /** Estimated probability of the MPS under the current state. */
  mpsProbability(): number {
    return 1 - approxLpsProbability(this.stateIdx);
  }
}

/** A fixed-size bank of independent contexts, one per modeling condition. */
export class ContextStore {
  readonly contexts: ContextModel[];

  constructor(size: number) {
    this.contexts = Array.from({ length: size }, () => new ContextModel());
  }

  get(idx: number): ContextModel {
    return this.contexts[idx];
  }

  reset(): void {
    for (const c of this.contexts) {
      c.stateIdx = 0;
      c.valMPS = 0;
    }
  }
}

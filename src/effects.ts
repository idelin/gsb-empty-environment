/**
 * Transfer effects inside a basic block and the guarded call shape.
 *
 * Runtime mutual recursion: block stepping resolves references through the
 * `readRef`/`writeRef` helpers imported from `model.ts`, while `model.ts`
 * imports this module's {@link stepBlock}.
 */
import { readRef, writeRef, type AbstractState, type Ref } from "./model.ts";
import { evaluatePredicate, type Predicate } from "./predicate.ts";
import type { DomainValue } from "./domain.ts";
import type { VerifiedSummary } from "./summary.ts";

export type Effect =
  | { readonly kind: "assign"; readonly target: Ref; readonly source: Ref }
  | {
      readonly kind: "setLabel";
      readonly target: Ref;
      readonly label: string;
    }
  | {
      readonly kind: "assume";
      readonly predicate: Predicate;
    };

/**
 * Call edges may only reference a previously verified callee summary. The
 * `VerifiedSummary` brand cannot be forged outside this package's verifier, so
 * unverified or failed declarations can never flow into a call node.
 */
export interface CallShape {
  readonly callee: string;
  readonly summary: VerifiedSummary;
  readonly args: ReadonlyArray<Ref>;
  readonly bindResult?: Ref;
  readonly bindObject?: Ref;
}

export interface BlockStep {
  readonly feasible: boolean;
  readonly state: AbstractState;
}

export function stepBlock(effects: ReadonlyArray<Effect>, initial: AbstractState): BlockStep {
  let state = initial;
  for (const effect of effects) {
    if (effect.kind === "assume") {
      if (!evaluatePredicate(effect.predicate, state)) {
        return { feasible: false, state };
      }
      continue;
    }
    if (effect.kind === "setLabel") {
      state = writeRef(effect.target, state, effect.label);
      continue;
    }
    const value: DomainValue = readRef(effect.source, state);
    state = writeRef(effect.target, state, value);
  }
  return { feasible: true, state };
}

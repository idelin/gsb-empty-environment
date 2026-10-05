/**
 * Nondeterministic finite automaton over the graph's label set.
 * Cycles are allowed. There is a single initial state and a set of
 * accepting states; the empty word is accepted iff the initial state
 * is accepting.
 */

import type { Label } from "./graph.ts";

export type AutomatonState = string;

export interface AutomatonTransition {
  readonly from: AutomatonState;
  readonly label: Label;
  readonly to: AutomatonState;
}

export interface AutomatonSpec {
  readonly states: Iterable<AutomatonState>;
  readonly initial: AutomatonState;
  readonly accepting: Iterable<AutomatonState>;
  readonly transitions: Iterable<AutomatonTransition>;
}

export class Automaton {
  readonly initial: AutomatonState;
  private readonly stateSet = new Set<AutomatonState>();
  private readonly acceptingSet = new Set<AutomatonState>();
  private readonly trans = new Map<AutomatonState, Map<Label, AutomatonState[]>>();

  constructor(spec: AutomatonSpec) {
    for (const s of spec.states) this.stateSet.add(s);
    for (const s of spec.accepting) {
      if (!this.stateSet.has(s)) throw new Error(`unknown accepting state: ${s}`);
      this.acceptingSet.add(s);
    }
    if (!this.stateSet.has(spec.initial)) {
      throw new Error(`unknown initial state: ${spec.initial}`);
    }
    this.initial = spec.initial;
    for (const t of spec.transitions) {
      if (!this.stateSet.has(t.from)) throw new Error(`unknown state: ${t.from}`);
      if (!this.stateSet.has(t.to)) throw new Error(`unknown state: ${t.to}`);
      let byLabel = this.trans.get(t.from);
      if (byLabel === undefined) {
        byLabel = new Map();
        this.trans.set(t.from, byLabel);
      }
      let succ = byLabel.get(t.label);
      if (succ === undefined) {
        succ = [];
        byLabel.set(t.label, succ);
      }
      if (!succ.includes(t.to)) succ.push(t.to);
    }
    // Deterministic expansion order: sort every successor list.
    for (const byLabel of this.trans.values()) {
      for (const succ of byLabel.values()) succ.sort();
    }
  }

  get states(): ReadonlySet<AutomatonState> {
    return this.stateSet;
  }

  get accepting(): ReadonlySet<AutomatonState> {
    return this.acceptingSet;
  }

  isAccepting(state: AutomatonState): boolean {
    return this.acceptingSet.has(state);
  }

  /** Successor automaton states for (state, label), sorted ascending. */
  step(state: AutomatonState, label: Label): readonly AutomatonState[] {
    return this.trans.get(state)?.get(label) ?? [];
  }
}

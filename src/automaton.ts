import { AutomatonSpec, AutomatonStateId, AutomatonTransition, Label } from "./types";

/** Nondeterministic finite automaton over the graph's edge labels. */
export class Automaton {
  readonly stateCount: number;
  private readonly initial: AutomatonStateId[];
  private readonly accepting = new Set<AutomatonStateId>();
  private readonly delta = new Map<AutomatonStateId, Map<Label, AutomatonStateId[]>>();

  constructor(spec: AutomatonSpec) {
    if (!Number.isInteger(spec.stateCount) || spec.stateCount <= 0) {
      throw new Error("stateCount must be a positive integer");
    }
    this.stateCount = spec.stateCount;
    const checkState = (q: AutomatonStateId): void => {
      if (!Number.isInteger(q) || q < 0 || q >= spec.stateCount) {
        throw new Error("automaton state out of range: " + q);
      }
    };
    this.initial = [];
    for (const q of spec.initialStates) {
      checkState(q);
      if (this.initial.indexOf(q) === -1) this.initial.push(q);
    }
    for (const q of spec.acceptingStates) {
      checkState(q);
      this.accepting.add(q);
    }
    for (const t of spec.transitions) {
      checkState(t.from);
      checkState(t.to);
      this.addTransition(t);
    }
    for (const byLabel of this.delta.values()) {
      for (const targets of byLabel.values()) targets.sort((a, b) => a - b);
    }
  }

  private addTransition(t: AutomatonTransition): void {
    let byLabel = this.delta.get(t.from);
    if (byLabel === undefined) {
      byLabel = new Map();
      this.delta.set(t.from, byLabel);
    }
    let targets = byLabel.get(t.label);
    if (targets === undefined) {
      targets = [];
      byLabel.set(t.label, targets);
    }
    if (targets.indexOf(t.to) === -1) targets.push(t.to);
  }

  initialStates(): AutomatonStateId[] {
    return this.initial.slice();
  }

  isInitial(q: AutomatonStateId): boolean {
    return this.initial.indexOf(q) !== -1;
  }

  isAccepting(q: AutomatonStateId): boolean {
    return this.accepting.has(q);
  }

  /** Successor automaton states of `q` under `label`, sorted ascending. */
  next(q: AutomatonStateId, label: Label): AutomatonStateId[] {
    const byLabel = this.delta.get(q);
    if (byLabel === undefined) return [];
    const targets = byLabel.get(label);
    return targets === undefined ? [] : targets.slice();
  }
}

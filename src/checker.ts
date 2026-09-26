// Independent model checker generated from the same Spec.
// It re-implements one-step semantics on its own (no executor code reuse) and
// explores the finite input domain breadth-first, so counterexamples are shortest.

import {
  modeMatches,
  stateHash,
  type Event,
  type Spec,
} from './spec.ts';

export type StepResult<S> =
  | { ok: true; state: S }
  | { ok: false; reason: string };

/** One-step semantics, implemented independently from the executor. */
export function step<S>(spec: Spec<S>, state: S, event: Event): StepResult<S> {
  const def = spec.events.find((e) => e.name === event.type);
  if (!def) return { ok: false, reason: `unknown event '${event.type}'` };
  if (spec.resolveTarget) {
    const reason = spec.resolveTarget(state, event);
    if (reason !== null) return { ok: false, reason };
  }
  const mode = spec.modeOf(state);
  if (!def.modes.some((p) => modeMatches(p, mode))) {
    return { ok: false, reason: `mode '${mode}' rejects '${event.type}'` };
  }
  if (def.guard) {
    const reason = def.guard(state, event);
    if (reason !== null) return { ok: false, reason };
  }
  return { ok: true, state: def.action(state, event) };
}

export interface Counterexample {
  readonly kind: 'invariant-violated' | 'unreachable-reached';
  readonly name: string;
  readonly detail: string;
  /** shortest event prefix leading to the offending state */
  readonly trace: readonly Event[];
  readonly state: unknown;
}

export interface CheckReport {
  readonly ok: boolean;
  readonly exploredStates: number;
  readonly maxDepth: number;
  readonly counterexample?: Counterexample;
}

/** Finite event domain: cartesian of each event def with its payload domain. */
export function inputDomain<S>(spec: Spec<S>): Event[] {
  const out: Event[] = [];
  for (const def of spec.events) {
    const payloads = def.domain ? def.domain() : [undefined];
    for (const payload of payloads) out.push({ type: def.name, payload });
  }
  return out;
}

export function check<S>(spec: Spec<S>, options: { maxDepth?: number; maxStates?: number } = {}): CheckReport {
  const maxDepth = options.maxDepth ?? 6;
  const maxStates = options.maxStates ?? 200_000;
  const domain = inputDomain(spec);

  interface Node {
    state: S;
    trace: Event[];
  }

  const violates = (state: S, trace: Event[]): Counterexample | null => {
    for (const inv of spec.invariants) {
      const detail = inv.check(state);
      if (detail !== null) {
        return { kind: 'invariant-violated', name: inv.name, detail, trace, state };
      }
    }
    for (const pred of spec.unreachable ?? []) {
      if (pred.matches(state)) {
        return {
          kind: 'unreachable-reached',
          name: pred.name,
          detail: `state matching '${pred.name}' is reachable`,
          trace,
          state,
        };
      }
    }
    return null;
  };

  const initial = spec.initial();
  const rootViolation = violates(initial, []);
  if (rootViolation) return { ok: false, exploredStates: 1, maxDepth, counterexample: rootViolation };

  // BFS => the first counterexample found uses a shortest event prefix.
  let frontier: Node[] = [{ state: initial, trace: [] }];
  const visited = new Set<string>([stateHash(initial)]);
  let explored = 1;

  for (let depth = 1; depth <= maxDepth; depth++) {
    const nextFrontier: Node[] = [];
    for (const node of frontier) {
      for (const event of domain) {
        const result = step(spec, node.state, event);
        if (!result.ok) continue;
        const hash = stateHash(result.state);
        if (visited.has(hash)) continue;
        visited.add(hash);
        explored += 1;
        if (explored > maxStates) {
          return { ok: true, exploredStates: explored, maxDepth };
        }
        const trace = [...node.trace, event];
        const violation = violates(result.state, trace);
        if (violation) {
          return { ok: false, exploredStates: explored, maxDepth, counterexample: violation };
        }
        nextFrontier.push({ state: result.state, trace });
      }
    }
    frontier = nextFrontier;
    if (frontier.length === 0) break;
  }
  return { ok: true, exploredStates: explored, maxDepth };
}

// Executor generated from a Spec: dispatch with guards + atomic actions,
// invariant checking, event recording / replay, transactional side effects,
// and hot-swapping of the spec with migration evidence.

import {
  modeMatches,
  modeExists,
  stateHash,
  type Event,
  type Spec,
} from './spec.ts';

export type DiagnosticKind =
  | 'unknown-event'
  | 'missing-target'
  | 'mode-mismatch'
  | 'guard-rejected'
  | 'invariant-violated'
  | 'effect-failed';

export interface Diagnostic {
  readonly seq: number;
  readonly event: Event;
  readonly kind: DiagnosticKind;
  readonly detail: string;
}

export interface TraceEntry {
  readonly seq: number;
  readonly event: Event;
  readonly stateHash: string;
}

export interface DispatchResult {
  readonly accepted: boolean;
  readonly diagnostic?: Diagnostic;
}

export interface Divergence {
  readonly seq: number;
  readonly event: Event;
  readonly diagnostic: Diagnostic;
}

export interface ReplayResult<S> {
  readonly state: S;
  readonly divergences: readonly Divergence[];
}

export interface MigrationEvidence {
  readonly fromSpec: string;
  readonly toSpec: string;
  readonly modeValid: boolean;
  readonly invariants: readonly { name: string; ok: boolean; detail: string | null }[];
}

export interface HotSwapResult {
  readonly ok: boolean;
  readonly evidence: MigrationEvidence;
  readonly error?: string;
}

export type Effect<S> = (next: S, event: Event, prev: S) => void;

export interface Executor<S> {
  dispatch(event: Event): DispatchResult;
  replay(events: readonly Event[]): ReplayResult<S>;
  hotSwap(newSpec: Spec<S>, migrate: (state: S) => S): HotSwapResult;
  getState(): S;
  getSpec(): Spec<S>;
  getTrace(): readonly TraceEntry[];
  getDiagnostics(): readonly Diagnostic[];
}

export function createExecutor<S>(
  spec: Spec<S>,
  options: { effects?: readonly Effect<S>[] } = {},
): Executor<S> {
  let currentSpec = spec;
  let state = spec.initial();
  let seq = 0;
  const trace: TraceEntry[] = [];
  const diagnostics: Diagnostic[] = [];
  const effects = options.effects ?? [];

  const reject = (event: Event, kind: DiagnosticKind, detail: string): DispatchResult => {
    const diagnostic: Diagnostic = { seq, event, kind, detail };
    diagnostics.push(diagnostic);
    return { accepted: false, diagnostic };
  };

  const checkInvariants = (s: S, event: Event): Diagnostic | null => {
    for (const inv of currentSpec.invariants) {
      const detail = inv.check(s);
      if (detail !== null) {
        return { seq, event, kind: 'invariant-violated', detail: `${inv.name}: ${detail}` };
      }
    }
    return null;
  };

  const dispatch = (event: Event): DispatchResult => {
    seq += 1;
    const def = currentSpec.events.find((e) => e.name === event.type);
    if (!def) return reject(event, 'unknown-event', `no event named '${event.type}'`);

    if (currentSpec.resolveTarget) {
      const reason = currentSpec.resolveTarget(state, event);
      if (reason !== null) return reject(event, 'missing-target', reason);
    }

    const mode = currentSpec.modeOf(state);
    if (!def.modes.some((pattern) => modeMatches(pattern, mode))) {
      return reject(event, 'mode-mismatch', `event '${event.type}' not allowed in mode '${mode}'`);
    }

    if (def.guard) {
      const reason = def.guard(state, event);
      if (reason !== null) return reject(event, 'guard-rejected', reason);
    }

    // Atomic multi-field action: the next state is computed in full before commit.
    let next: S;
    try {
      next = def.action(state, event);
    } catch (err) {
      return reject(event, 'guard-rejected', `action threw: ${(err as Error).message}`);
    }

    const invFailure = checkInvariants(next, event);
    if (invFailure) {
      diagnostics.push(invFailure);
      return { accepted: false, diagnostic: invFailure };
    }

    // Transaction convention: side effects run before commit; any failure rolls
    // the whole transaction back — the pre-event state is kept.
    try {
      for (const effect of effects) effect(next, event, state);
    } catch (err) {
      return reject(event, 'effect-failed', `rolled back: ${(err as Error).message}`);
    }

    state = next;
    trace.push({ seq, event, stateHash: stateHash(state) });
    return { accepted: true };
  };

  const replay = (events: readonly Event[]): ReplayResult<S> => {
    state = currentSpec.initial();
    trace.length = 0;
    diagnostics.length = 0;
    seq = 0;
    const divergences: Divergence[] = [];
    for (const event of events) {
      const result = dispatch(event);
      if (!result.accepted && result.diagnostic) {
        divergences.push({ seq: result.diagnostic.seq, event, diagnostic: result.diagnostic });
      }
    }
    return { state, divergences };
  };

  const hotSwap = (newSpec: Spec<S>, migrate: (state: S) => S): HotSwapResult => {
    const base: MigrationEvidence = {
      fromSpec: currentSpec.name,
      toSpec: newSpec.name,
      modeValid: false,
      invariants: [],
    };
    let migrated: S;
    try {
      migrated = migrate(state);
    } catch (err) {
      // Failure: keep old spec and old state untouched.
      return { ok: false, evidence: base, error: `migration threw: ${(err as Error).message}` };
    }
    const evidence: MigrationEvidence = {
      ...base,
      modeValid: modeExists(newSpec.modeRoot, newSpec.modeOf(migrated)),
      invariants: newSpec.invariants.map((inv) => {
        const detail = inv.check(migrated);
        return { name: inv.name, ok: detail === null, detail };
      }),
    };
    const ok = evidence.modeValid && evidence.invariants.every((r) => r.ok);
    if (!ok) {
      // Evidence does not hold: old spec and state stay in charge.
      return { ok: false, evidence, error: 'migration evidence failed' };
    }
    currentSpec = newSpec;
    state = migrated;
    trace.length = 0;
    diagnostics.length = 0;
    return { ok: true, evidence };
  };

  return {
    dispatch,
    replay,
    hotSwap,
    getState: () => state,
    getSpec: () => currentSpec,
    getTrace: () => [...trace],
    getDiagnostics: () => [...diagnostics],
  };
}

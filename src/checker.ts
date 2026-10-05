// Trace checker for a fixed 5-member consensus implementation.
//
// Pass 1 replays promise / persist / accept / restart events per node and
// enforces the durable-state rules. Pass 2 replays learn / apply events and
// enforces quorum and apply-order rules. Violations carry minimal causal
// evidence; findings that depend on missing causal events are reported as
// incomplete instead — missing logs never convict the implementation.

import type {
  AcceptedValue,
  Ballot,
  CheckResult,
  Finding,
  FindingKind,
  TraceEvent,
  ViolationClaim,
} from './types.ts';
import { QUORUM } from './types.ts';
import { compareBallot, ballotEq, ballotToString } from './ballot.ts';
import { ingestTrace } from './trace.ts';
import { causalClosure, minimizeEvidence, DEFAULT_BUDGET } from './evidence.ts';

export interface CheckOptions {
  budget?: number; // verifier-call budget for evidence minimization
}

interface NodeDurableState {
  persistedPromise: Ballot | null;
  persistedPromiseFrom: string | null;
  persistedAccepted: Map<number, { value: AcceptedValue; from: string }>;
  persistEventIds: string[];
}

interface NodeApplyState {
  appliedUpTo: number;
  appliedValues: Map<number, string>;
  priorApplyIds: string[];
}

export function checkTrace(
  rawEvents: unknown[],
  members: string[],
  options: CheckOptions = {},
): CheckResult {
  const budget = options.budget ?? DEFAULT_BUDGET;
  const trace = ingestTrace(rawEvents, members);
  const { events, byId, indexOf, duplicatesIgnored } = trace;

  const violations: Finding[] = [];
  const incomplete: Finding[] = [];

  const missingCausesOf = (e: TraceEvent): string[] =>
    e.causes.filter((c) => !byId.has(c)).sort();

  const addViolation = (
    kind: FindingKind,
    claim: ViolationClaim,
    rootIds: string[],
    detail: string,
  ): void => {
    const closure = causalClosure(rootIds, byId, indexOf);
    const evidence = minimizeEvidence(closure.events, claim, budget);
    violations.push({
      kind,
      severity: 'violation',
      eventIds: [...rootIds],
      missingCauses: closure.missing,
      detail,
      evidence,
    });
  };

  const addIncomplete = (
    kind: FindingKind,
    rootIds: string[],
    missing: string[],
    detail: string,
  ): void => {
    incomplete.push({ kind, severity: 'incomplete', eventIds: [...rootIds], missingCauses: missing, detail });
  };

  // ---- pass 1: durable-state rules ----------------------------------------

  const durable = new Map<string, NodeDurableState>();
  const durableOf = (node: string): NodeDurableState => {
    let st = durable.get(node);
    if (!st) {
      st = {
        persistedPromise: null,
        persistedPromiseFrom: null,
        persistedAccepted: new Map(),
        persistEventIds: [],
      };
      durable.set(node, st);
    }
    return st;
  };

  const acceptValidity = new Map<string, boolean>();
  const acceptInvalidatedBy = new Map<string, string>();

  /** Checker-side mirror of the restart coverage rule (kept separate from the verifier). */
  const recoveredCoversPersisted = (
    recovered: NonNullable<TraceEvent['recovered']>,
    st: NodeDurableState,
  ): string[] => {
    const problems: string[] = [];
    if (st.persistedPromise) {
      const ok =
        recovered.promised !== undefined &&
        compareBallot(recovered.promised, st.persistedPromise) >= 0;
      if (!ok) {
        problems.push(`persisted promise ${ballotToString(st.persistedPromise)} not recovered`);
      }
    }
    for (const [slot, entry] of st.persistedAccepted) {
      const match = (recovered.accepted ?? []).find((x) => x.slot === slot);
      const ok =
        match !== undefined &&
        match.value === entry.value.value &&
        compareBallot(match.ballot, entry.value.ballot) >= 0;
      if (!ok) {
        problems.push(
          `persisted accept slot ${slot} value "${entry.value.value}" ` +
            `ballot ${ballotToString(entry.value.ballot)} not recovered`,
        );
      }
    }
    return problems;
  };

  for (const e of events) {
    switch (e.kind) {
      case 'promise':
        break; // volatile promises impose no durable constraint
      case 'persist': {
        const st = durableOf(e.node);
        const ps = e.persisted as NonNullable<TraceEvent['persisted']>;
        if (
          ps.promised &&
          (!st.persistedPromise || compareBallot(ps.promised, st.persistedPromise) > 0)
        ) {
          st.persistedPromise = ps.promised;
          st.persistedPromiseFrom = e.id;
        }
        for (const acc of ps.accepted ?? []) {
          const cur = st.persistedAccepted.get(acc.slot);
          if (!cur || compareBallot(acc.ballot, cur.value.ballot) > 0) {
            st.persistedAccepted.set(acc.slot, { value: acc, from: e.id });
          }
        }
        st.persistEventIds.push(e.id);
        break;
      }
      case 'accept': {
        const st = durableOf(e.node);
        let valid = true;
        if (
          st.persistedPromise &&
          e.ballot &&
          compareBallot(e.ballot, st.persistedPromise) < 0
        ) {
          valid = false;
          acceptInvalidatedBy.set(e.id, st.persistedPromiseFrom as string);
          addViolation(
            'AcceptBelowPersistedPromise',
            { kind: 'AcceptBelowPersistedPromise', acceptId: e.id },
            [e.id, st.persistedPromiseFrom as string],
            `accept ${e.id} at ballot ${ballotToString(e.ballot)} is below persisted promise ` +
              `${ballotToString(st.persistedPromise)} (persist ${st.persistedPromiseFrom}) on ${e.node}`,
          );
        }
        acceptValidity.set(e.id, valid);
        break;
      }
      case 'restart': {
        const st = durableOf(e.node);
        const problems = recoveredCoversPersisted(
          e.recovered as NonNullable<TraceEvent['recovered']>,
          st,
        );
        if (problems.length > 0) {
          addViolation(
            'RestartLostPersistedState',
            { kind: 'RestartLostPersistedState', restartId: e.id },
            [e.id, ...st.persistEventIds],
            `restart ${e.id} on ${e.node} lost confirmed durable state: ${problems.join('; ')}`,
          );
        }
        break;
      }
      default:
        break; // learn / apply handled in pass 2
    }
  }

  // ---- pass 2: quorum and apply-order rules --------------------------------

  const acceptsMatching = (
    slot: number,
    value: string,
    ballot: Ballot,
  ): TraceEvent[] =>
    events.filter(
      (x) =>
        x.kind === 'accept' &&
        x.slot === slot &&
        x.value === value &&
        x.ballot !== undefined &&
        ballotEq(x.ballot, ballot),
    );

  const justification = (
    slot: number,
    value: string,
    ballot: Ballot,
  ): { validNodes: Set<string>; matching: TraceEvent[] } => {
    const matching = acceptsMatching(slot, value, ballot);
    const validNodes = new Set<string>();
    for (const a of matching) {
      if (acceptValidity.get(a.id) !== false) validNodes.add(a.node);
    }
    return { validNodes, matching };
  };

  const invalidatingPersistsOf = (accepts: TraceEvent[]): string[] => {
    const ids = new Set<string>();
    for (const a of accepts) {
      const p = acceptInvalidatedBy.get(a.id);
      if (p) ids.add(p);
    }
    return [...ids];
  };

  const chosenBySlot = new Map<number, { value: string; learnId: string }>();
  const applyStates = new Map<string, NodeApplyState>();
  const applyStateOf = (node: string): NodeApplyState => {
    let st = applyStates.get(node);
    if (!st) {
      st = { appliedUpTo: 0, appliedValues: new Map(), priorApplyIds: [] };
      applyStates.set(node, st);
    }
    return st;
  };

  for (const e of events) {
    if (e.kind === 'learn') {
      const slot = e.slot as number;
      const value = e.value as string;
      const ballot = e.ballot as Ballot;
      const { validNodes, matching } = justification(slot, value, ballot);
      if (validNodes.size >= QUORUM) {
        const cur = chosenBySlot.get(slot);
        if (cur && cur.value !== value) {
          const other = byId.get(cur.learnId) as TraceEvent;
          const otherMatching = acceptsMatching(
            slot,
            cur.value,
            other.ballot as Ballot,
          );
          addViolation(
            'ConflictingChosen',
            { kind: 'ConflictingChosen', slot },
            [e.id, cur.learnId, ...otherMatching.map((a) => a.id), ...matching.map((a) => a.id)],
            `slot ${slot} chosen with conflicting values "${cur.value}" (learn ${cur.learnId}) ` +
              `and "${value}" (learn ${e.id})`,
          );
        } else if (!cur) {
          chosenBySlot.set(slot, { value, learnId: e.id });
        }
      } else {
        const missing = missingCausesOf(e);
        const roots = [
          e.id,
          ...matching.map((a) => a.id),
          ...invalidatingPersistsOf(matching),
        ];
        if (missing.length > 0) {
          addIncomplete(
            'UnquorumedLearn',
            [e.id],
            missing,
            `learn ${e.id} references missing causal events ${missing.join(', ')}; ` +
              'a quorum cannot be confirmed or ruled out',
          );
        } else {
          addViolation(
            'UnquorumedLearn',
            { kind: 'UnquorumedLearn', learnId: e.id },
            roots,
            `learn ${e.id} for slot ${slot} value "${value}" has only ${validNodes.size} ` +
              `distinct valid accepts (< ${QUORUM})`,
          );
        }
      }
      continue;
    }

    if (e.kind === 'apply') {
      const slot = e.slot as number;
      const value = e.value as string;
      const st = applyStateOf(e.node);

      if (slot <= st.appliedUpTo && st.appliedValues.get(slot) === value) {
        continue; // idempotent re-apply
      }
      if (slot !== st.appliedUpTo + 1) {
        addViolation(
          'ApplyGap',
          { kind: 'ApplyGap', applyId: e.id },
          [e.id, ...st.priorApplyIds],
          `apply ${e.id} on ${e.node} applies slot ${slot} while the applied prefix ` +
            `ends at ${st.appliedUpTo}`,
        );
        continue; // the applied prefix does not advance across a gap
      }

      // In-order apply: verify the value was actually chosen.
      const learnRefs = e.causes
        .map((c) => byId.get(c))
        .filter((x): x is TraceEvent => x !== undefined && x.kind === 'learn');
      const matching = learnRefs.find((l) => l.slot === slot && l.value === value);
      const mismatched = learnRefs.find((l) => l.slot !== slot || l.value !== value);

      if (matching) {
        const { validNodes, matching: quorumAccepts } = justification(
          slot,
          value,
          matching.ballot as Ballot,
        );
        if (validNodes.size < QUORUM) {
          addViolation(
            'ApplyNotChosen',
            { kind: 'ApplyNotChosen', applyId: e.id },
            [
              e.id,
              matching.id,
              ...quorumAccepts.map((a) => a.id),
              ...invalidatingPersistsOf(quorumAccepts),
            ],
            `apply ${e.id} on ${e.node} applies slot ${slot} value "${value}" justified by ` +
              `learn ${matching.id}, which lacks a quorum`,
          );
        }
      } else if (mismatched) {
        addViolation(
          'ApplyValueMismatch',
          { kind: 'ApplyValueMismatch', applyId: e.id },
          [e.id, ...learnRefs.map((l) => l.id)],
          `apply ${e.id} on ${e.node} applies slot ${slot} value "${value}" but references ` +
            `learn ${mismatched.id} for slot ${mismatched.slot} value "${mismatched.value}"`,
        );
      } else {
        addIncomplete(
          'ApplyNotChosen',
          [e.id],
          missingCausesOf(e),
          `apply ${e.id} on ${e.node} has no usable learn reference; ` +
            'chosenness cannot be confirmed from the available logs',
        );
      }

      st.appliedUpTo = slot;
      st.appliedValues.set(slot, value);
      st.priorApplyIds.push(e.id);
    }
  }

  return {
    ok: violations.length === 0,
    violations,
    incomplete,
    stats: { events: events.length, duplicatesIgnored },
  };
}

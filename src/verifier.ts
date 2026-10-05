// Independent evidence verifier.
//
// This module is deliberately self-contained: it re-derives every rule from
// nothing but the evidence events it is given (interpreted in array order,
// which must be the original trace order) plus the claim. It shares no logic
// with the checker, so a bug in the checker cannot silently validate itself.

import type { DurableState, TraceEvent, ViolationClaim } from './types.ts';
import { QUORUM } from './types.ts';
import { compareBallot, ballotEq } from './ballot.ts';

function persistsBefore(events: TraceEvent[], node: string, beforeIdx: number): TraceEvent[] {
  const out: TraceEvent[] = [];
  for (let i = 0; i < beforeIdx; i++) {
    const e = events[i];
    if (e.kind === 'persist' && e.node === node) out.push(e);
  }
  return out;
}

/** An accept is usable unless an earlier persist in the evidence proves a higher promise. */
function acceptIsValid(events: TraceEvent[], acceptIdx: number): boolean {
  const accept = events[acceptIdx];
  if (!accept.ballot) return false;
  for (const p of persistsBefore(events, accept.node, acceptIdx)) {
    const promised = p.persisted?.promised;
    if (promised && compareBallot(accept.ballot, promised) < 0) return false;
  }
  return true;
}

/** Distinct members with a valid accept matching (slot, value, ballot). */
function quorumNodes(
  events: TraceEvent[],
  slot: number,
  value: string,
  ballot: { round: number; node: string },
): Set<string> {
  const nodes = new Set<string>();
  events.forEach((e, i) => {
    if (
      e.kind === 'accept' &&
      e.slot === slot &&
      e.value === value &&
      e.ballot !== undefined &&
      ballotEq(e.ballot, ballot) &&
      acceptIsValid(events, i)
    ) {
      nodes.add(e.node);
    }
  });
  return nodes;
}

function learnIsJustified(events: TraceEvent[], learn: TraceEvent): boolean {
  if (learn.slot === undefined || learn.value === undefined || learn.ballot === undefined) {
    return false;
  }
  return quorumNodes(events, learn.slot, learn.value, learn.ballot).size >= QUORUM;
}

/** Does the recovered state cover everything the persist event confirmed durable? */
function recoveredCovers(recovered: DurableState, persisted: DurableState): boolean {
  if (persisted.promised !== undefined) {
    if (recovered.promised === undefined) return false;
    if (compareBallot(recovered.promised, persisted.promised) < 0) return false;
  }
  for (const acc of persisted.accepted ?? []) {
    const match = (recovered.accepted ?? []).find((x) => x.slot === acc.slot);
    if (!match) return false;
    if (match.value !== acc.value) return false;
    if (compareBallot(match.ballot, acc.ballot) < 0) return false;
  }
  return true;
}

function findById(events: TraceEvent[], id: string): { event: TraceEvent; idx: number } | null {
  const idx = events.findIndex((e) => e.id === id);
  return idx < 0 ? null : { event: events[idx], idx };
}

/**
 * Returns true iff the given events, on their own, prove the claimed violation.
  * Missing information always counts against the claim, never against the
 * implementation under review.
 */
export function verifyEvidence(events: TraceEvent[], claim: ViolationClaim): boolean {
  switch (claim.kind) {
    case 'AcceptBelowPersistedPromise': {
      const found = findById(events, claim.acceptId);
      if (!found) return false;
      const { event: accept, idx } = found;
      if (accept.kind !== 'accept' || accept.ballot === undefined) return false;
      for (const p of persistsBefore(events, accept.node, idx)) {
        const promised = p.persisted?.promised;
        if (promised && compareBallot(accept.ballot, promised) < 0) return true;
      }
      return false;
    }

    case 'RestartLostPersistedState': {
      const found = findById(events, claim.restartId);
      if (!found) return false;
      const { event: restart, idx } = found;
      if (restart.kind !== 'restart' || restart.recovered === undefined) return false;
      for (const p of persistsBefore(events, restart.node, idx)) {
        if (p.persisted && !recoveredCovers(restart.recovered, p.persisted)) return true;
      }
      return false;
    }

    case 'UnquorumedLearn': {
      const found = findById(events, claim.learnId);
      if (!found) return false;
      const learn = found.event;
      if (learn.kind !== 'learn') return false;
      // Provable only when every causal reference is present in the evidence;
      // otherwise missing logs might be hiding the quorum.
      const ids = new Set(events.map((e) => e.id));
      if (!learn.causes.every((c) => ids.has(c))) return false;
      return !learnIsJustified(events, learn);
    }

    case 'ConflictingChosen': {
      const chosenValues = new Set<string>();
      for (const e of events) {
        if (e.kind === 'learn' && e.slot === claim.slot && e.value !== undefined) {
          if (learnIsJustified(events, e)) chosenValues.add(e.value);
        }
      }
      return chosenValues.size >= 2;
    }

    case 'ApplyGap': {
      const found = findById(events, claim.applyId);
      if (!found) return false;
      const { event: apply, idx } = found;
      if (apply.kind !== 'apply' || apply.slot === undefined || apply.value === undefined) {
        return false;
      }
      let appliedUpTo = 0;
      const appliedValues = new Map<number, string>();
      for (let i = 0; i < idx; i++) {
        const e = events[i];
        if (e.kind !== 'apply' || e.node !== apply.node) continue;
        if (e.slot === undefined || e.value === undefined) continue;
        if (e.slot === appliedUpTo + 1) {
          appliedUpTo = e.slot;
          appliedValues.set(e.slot, e.value);
        } else if (e.slot <= appliedUpTo && appliedValues.get(e.slot) === e.value) {
          // idempotent re-apply, no state change
        }
        // an earlier gap does not advance the applied prefix
      }
      if (apply.slot === appliedUpTo + 1) return false;
      if (apply.slot <= appliedUpTo && appliedValues.get(apply.slot) === apply.value) {
        return false; // idempotent re-apply
      }
      return true;
    }

    case 'ApplyValueMismatch': {
      const found = findById(events, claim.applyId);
      if (!found) return false;
      const apply = found.event;
      if (apply.kind !== 'apply') return false;
      for (const causeId of apply.causes) {
        const cause = events.find((e) => e.id === causeId);
        if (cause && cause.kind === 'learn') {
          if (cause.slot !== apply.slot || cause.value !== apply.value) return true;
        }
      }
      return false;
    }

    case 'ApplyNotChosen': {
      const found = findById(events, claim.applyId);
      if (!found) return false;
      const apply = found.event;
      if (apply.kind !== 'apply') return false;
      const ids = new Set(events.map((e) => e.id));
      for (const causeId of apply.causes) {
        const cause = events.find((e) => e.id === causeId);
        if (
          cause &&
          cause.kind === 'learn' &&
          cause.slot === apply.slot &&
          cause.value === apply.value
        ) {
          const causesPresent = cause.causes.every((c) => ids.has(c));
          if (causesPresent && !learnIsJustified(events, cause)) return true;
        }
      }
      return false;
    }
  }
}

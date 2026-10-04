// Independent evidence verifier.
//
// This module is intentionally self-contained: it does not import any of the
// checker's analysis code (only shared type definitions). Given a claim and a
// small set of evidence events, it re-derives from first principles whether
// the evidence alone proves the claimed safety violation. Evidence that relies
// on events outside the given set fails closedness checks, so a verdict here
// can never depend on logs that are not part of the evidence.
import type { TraceEvent, Claim, VerifyResult } from './types';

const QUORUM: number = 3;

function indexById(events: TraceEvent[]): Map<string, TraceEvent> {
  const m: Map<string, TraceEvent> = new Map();
  for (const e of events) m.set(e.id, e);
  return m;
}

function pastOf(ids: string[], index: Map<string, TraceEvent>): Set<string> {
  const seen: Set<string> = new Set();
  const stack: string[] = ids.slice();
  while (stack.length > 0) {
    const id = stack.pop();
    if (id === undefined) break;
    if (seen.has(id)) continue;
    const e = index.get(id);
    if (!e) continue;
    seen.add(id);
    for (const c of e.causes) stack.push(c);
  }
  return seen;
}

function missingWithin(ids: string[], index: Map<string, TraceEvent>): Set<string> {
  const missing: Set<string> = new Set();
  const seen: Set<string> = new Set();
  const stack: string[] = ids.slice();
  while (stack.length > 0) {
    const id = stack.pop();
    if (id === undefined) break;
    if (seen.has(id)) continue;
    seen.add(id);
    const e = index.get(id);
    if (!e) {
      missing.add(id);
      continue;
    }
    for (const c of e.causes) stack.push(c);
  }
  return missing;
}

function wellFormedBallot(ballot: number): boolean {
  return typeof ballot === 'number' && Number.isInteger(ballot) && ballot >= 0;
}

function ballotBearing(kind: string): boolean {
  return kind === 'promise' || kind === 'accept' || kind === 'persist' || kind === 'learn';
}

function quorumSizeFor(learn: TraceEvent, events: TraceEvent[], index: Map<string, TraceEvent>): number {
  const past = pastOf([learn.id], index);
  const nodes: Set<string> = new Set();
  for (const e of events) {
    if (e.kind !== 'accept') continue;
    if (!past.has(e.id)) continue;
    if (!wellFormedBallot(e.ballot)) continue;
    if (e.slot === learn.slot && e.value === learn.value && e.ballot <= learn.ballot) {
      nodes.add(e.node);
    }
  }
  return nodes.size;
}

function ok(reason: string): VerifyResult {
  return { ok: true, reason };
}

function no(reason: string): VerifyResult {
  return { ok: false, reason };
}

export function verifyEvidence(events: TraceEvent[], claim: Claim): VerifyResult {
  const index = indexById(events);
  for (const f of claim.focus) {
    if (!index.has(f)) return no('focus event not in evidence: ' + f);
  }

  if (claim.kind === 'invalid_ballot') {
    for (const e of events) {
      if (ballotBearing(e.kind) && !wellFormedBallot(e.ballot)) {
        return ok('event ' + e.id + ' carries a ballot outside the total order');
      }
    }
    return no('no event with a malformed ballot in evidence');
  }

  if (claim.kind === 'accept_below_promise') {
    for (const a of events) {
      if (a.kind !== 'accept') continue;
      const past = pastOf([a.id], index);
      for (const p of events) {
        if (p.kind !== 'persist') continue;
        if (p.node !== a.node) continue;
        if (p.ballot > a.ballot && past.has(p.id)) {
          return ok('accept ' + a.id + ' at ballot ' + a.ballot + ' is below persisted promise ' + p.ballot);
        }
      }
    }
    return no('no accept below a causally prior persisted promise in evidence');
  }

  if (claim.kind === 'restart_state_loss') {
    for (const e of events) {
      if (e.kind !== 'accept' && e.kind !== 'promise') continue;
      const pastE = pastOf([e.id], index);
      for (const r of events) {
        if (r.kind !== 'restart' || r.node !== e.node) continue;
        if (!pastE.has(r.id)) continue;
        const pastR = pastOf([r.id], index);
        for (const p of events) {
          if (p.kind !== 'persist' || p.node !== e.node) continue;
          if (!pastR.has(p.id)) continue;
          if (e.ballot < p.ballot) {
            return ok('event ' + e.id + ' after restart ' + r.id + ' dropped persisted promise ballot ' + p.ballot);
          }
          if (e.kind === 'accept' && p.value !== null && p.slot >= 1 && p.slot === e.slot && p.value !== e.value) {
            return ok('accept ' + e.id + ' after restart ' + r.id + ' overwrote persisted value at slot ' + p.slot);
          }
        }
      }
    }
    return no('no post-restart state loss in evidence');
  }

  if (claim.kind === 'learn_without_quorum') {
    for (const l of events) {
      if (l.kind !== 'learn') continue;
      if (missingWithin([l.id], index).size > 0) continue;
      if (quorumSizeFor(l, events, index) < QUORUM) {
        return ok('learn ' + l.id + ' has fewer than ' + QUORUM + ' distinct valid acceptors in its closed causal past');
      }
    }
    return no('no closed-past learn without a quorum in evidence');
  }

  if (claim.kind === 'conflicting_chosen') {
    const learns: TraceEvent[] = events.filter((e: TraceEvent) => e.kind === 'learn');
    for (const l1 of learns) {
      for (const l2 of learns) {
        if (l1.id >= l2.id) continue;
        if (l1.slot !== l2.slot) continue;
        if (l1.value === l2.value) continue;
        if (quorumSizeFor(l1, events, index) >= QUORUM && quorumSizeFor(l2, events, index) >= QUORUM) {
          return ok('slot ' + l1.slot + ' chosen as both ' + l1.value + ' and ' + l2.value);
        }
      }
    }
    return no('no two quorum-backed conflicting learns in evidence');
  }

  if (claim.kind === 'apply_out_of_order') {
    for (const a of events) {
      if (a.kind !== 'apply') continue;
      if (a.slot < 1) return ok('apply ' + a.id + ' uses slot < 1');
      const past = pastOf([a.id], index);
      for (const pa of events) {
        if (pa.kind !== 'apply' || pa.node !== a.node || pa.id === a.id) continue;
        if (past.has(pa.id) && pa.slot >= a.slot) {
          return ok('apply ' + a.id + ' at slot ' + a.slot + ' follows apply ' + pa.id + ' at slot ' + pa.slot);
        }
      }
      if (a.slot > 1 && missingWithin([a.id], index).size === 0) {
        let hasPrev = false;
        for (const pa of events) {
          if (pa.kind === 'apply' && pa.node === a.node && past.has(pa.id) && pa.slot === a.slot - 1) {
            hasPrev = true;
            break;
          }
        }
        if (!hasPrev) return ok('apply ' + a.id + ' at slot ' + a.slot + ' skips slot ' + (a.slot - 1));
      }
    }
    return no('no out-of-order apply in evidence');
  }

  if (claim.kind === 'apply_without_chosen') {
    for (const a of events) {
      if (a.kind !== 'apply') continue;
      if (missingWithin([a.id], index).size > 0) continue;
      const past = pastOf([a.id], index);
      let chosen = false;
      for (const l of events) {
        if (l.kind === 'learn' && past.has(l.id) && l.slot === a.slot && l.value === a.value) {
          chosen = true;
          break;
        }
      }
      if (!chosen) return ok('apply ' + a.id + ' has no chosen learn for slot ' + a.slot + ' in its closed causal past');
    }
    return no('no apply without a chosen value in evidence');
  }

  return no('unknown claim kind');
}

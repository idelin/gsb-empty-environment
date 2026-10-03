import { AuditTrail } from './auditTrail.ts';
import { LeaseArbiter } from './arbiter.ts';
import { LeaseNode } from './leaseNode.ts';
import type { LeaseEvent } from './events.ts';

export interface RaceOptions {
  leaseId?: string;
  nodeA?: string;
  nodeB?: string;
  /** Wall-clock base time (ms) of the race. */
  baseMs?: number;
  /** Arrival gap between the two competing requests, in ms (sub-ms = race). */
  gapMs?: number;
  ttlMs?: number;
  /** Contention window of the arbiter, in ms. */
  contentionWindowMs?: number;
}

export interface RaceResult {
  leaseId: string;
  winner: string;
  loser: string;
  trail: AuditTrail;
  arbiter: LeaseArbiter;
  requestEvents: LeaseEvent[];
  contentionEvent: LeaseEvent | undefined;
  grantEvent: LeaseEvent;
  rejectionEvent: LeaseEvent;
  lockTransitions: LeaseEvent[];
  /** Causal evidence chain explaining the rejection. */
  evidenceChain: LeaseEvent[];
}

/**
 * Reproduce the classic master-lease race: two nodes fire acquire requests
 * at (nearly) the same millisecond. Both requests land in the arbiter's
 * inbox before either is processed, so the trail captures the true arrival
 * order; the arbiter then grants the first and rejects the second.
 */
export function simulateMasterLeaseRace(opts: RaceOptions = {}): RaceResult {
  const leaseId = opts.leaseId ?? 'master';
  const nodeA = opts.nodeA ?? 'node-A';
  const nodeB = opts.nodeB ?? 'node-B';
  const baseMs = opts.baseMs ?? 1_000;
  const gapMs = opts.gapMs ?? 0.42;
  const ttlMs = opts.ttlMs ?? 5_000;

  const trail = new AuditTrail();
  // Processing starts only after the last competing request has arrived.
  let nowMs = baseMs + gapMs + 0.1;
  const arbiter = new LeaseArbiter(trail, {
    contentionWindowMs: opts.contentionWindowMs ?? 1,
    now: () => nowMs,
  });
  const a = new LeaseNode(nodeA);
  const b = new LeaseNode(nodeB);

  // Both nodes "simultaneously" fire: requests are submitted (and recorded)
  // before the arbiter processes anything, mirroring real network arrival.
  const reqA = a.send(arbiter, 'acquire', leaseId, baseMs, { ttlMs });
  const reqB = b.send(arbiter, 'acquire', leaseId, baseMs + gapMs, { ttlMs });

  const outcomes = arbiter.drain();
  const grant = outcomes.find((o) => o.event.type === 'LEASE_GRANTED');
  const rejection = outcomes.find((o) => o.event.type === 'LEASE_REJECTED');
  const contention = trail.byType('CONTENTION_DETECTED')[0];
  if (!grant || !rejection) {
    throw new Error('race simulation did not produce grant + rejection');
  }

  return {
    leaseId,
    winner: grant.event.nodeId!,
    loser: rejection.event.nodeId!,
    trail,
    arbiter,
    requestEvents: [reqA, reqB],
    contentionEvent: contention,
    grantEvent: grant.event,
    rejectionEvent: rejection.event,
    lockTransitions: trail.byType('LOCK_STATE_CHANGED'),
    evidenceChain: trail.causalChain(rejection.event.id),
  };
}

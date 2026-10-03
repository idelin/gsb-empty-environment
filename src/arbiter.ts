import { LamportClock } from './logicalClock.ts';
import { AuditTrail } from './auditTrail.ts';
import type { LeaseEvent, LockState, RequestOp } from './events.ts';

export interface InboundRequest {
  op: RequestOp;
  leaseId: string;
  nodeId: string;
  /** Lamport timestamp stamped by the sending node. */
  senderLamport: number;
  /** Wall-clock arrival time at the arbiter (ms, float). */
  arriveMs: number;
  ttlMs?: number;
  /** Target node for transfer operations. */
  toNodeId?: string;
}

export interface ArbiterOptions {
  /**
   * Two acquire requests for the same lease arriving within this many
   * milliseconds are flagged as a contention (race) in the trail.
   */
  contentionWindowMs?: number;
  /** Wall-clock source, injectable for deterministic tests. */
  now?: () => number;
}

interface LockRecord {
  state: LockState;
  holder?: string;
  version: number;
  expiresAtMs?: number;
  grantedEventId?: string;
}

interface QueuedRequest {
  req: InboundRequest;
  requestEventId: string;
}

export interface Outcome {
  ok: boolean;
  event: LeaseEvent;
}

/**
 * Lease arbiter with an explicit inbound queue.
 *
 * Requests are time-stamped and appended to the audit trail the instant
 * they ARRIVE (`submit`), then processed one at a time (`processNext`).
 * That split is what makes millisecond races observable: both competing
 * requests are durably recorded, in arrival order, before the arbiter's
 * lock state machine serializes a winner and rejects the loser.
 */
export class LeaseArbiter {
  readonly clock = new LamportClock();
  private readonly trail: AuditTrail;
  private readonly contentionWindowMs: number;
  private readonly now: () => number;
  private readonly locks = new Map<string, LockRecord>();
  private readonly inbox: QueuedRequest[] = [];
  private readonly contentionPairs = new Set<string>();

  constructor(trail: AuditTrail, opts: ArbiterOptions = {}) {
    this.trail = trail;
    this.contentionWindowMs = opts.contentionWindowMs ?? 1;
    this.now = opts.now ?? (() => performance.now());
  }

  /** Current lock state of a lease (UNLOCKED if never granted). */
  lockState(leaseId: string): LockRecord {
    return { ...(this.locks.get(leaseId) ?? { state: 'UNLOCKED' as LockState, version: 0 }) };
  }

  get pending(): number {
    return this.inbox.length;
  }

  /** Record a request arrival. Returns the recorded LEASE_REQUESTED event. */
  submit(req: InboundRequest): LeaseEvent {
    const lamport = this.clock.merge(req.senderLamport);
    const requestEvent = this.trail.record({
      lamport,
      wallClockMs: req.arriveMs,
      type: 'LEASE_REQUESTED',
      leaseId: req.leaseId,
      nodeId: req.nodeId,
      causedBy: [],
      details: {
        op: req.op,
        ttlMs: req.ttlMs,
        ...(req.toNodeId ? { toNodeId: req.toNodeId } : {}),
        senderLamport: req.senderLamport,
      },
    });
    if (req.op === 'acquire') this.detectContention(req, requestEvent);
    this.inbox.push({ req, requestEventId: requestEvent.id });
    return requestEvent;
  }

  /** Process the oldest queued request, if any. */
  processNext(): Outcome | undefined {
    const item = this.inbox.shift();
    if (!item) return undefined;
    const { req, requestEventId } = item;
    this.clock.tick();
    switch (req.op) {
      case 'acquire': return this.handleAcquire(req, requestEventId);
      case 'renew': return this.handleRenew(req, requestEventId);
      case 'transfer': return this.handleTransfer(req, requestEventId);
      case 'release': return this.handleRelease(req, requestEventId);
    }
  }

  /** Process every queued request in arrival order. */
  drain(): Outcome[] {
    const out: Outcome[] = [];
    let o: Outcome | undefined;
    while ((o = this.processNext())) out.push(o);
    return out;
  }

  /** Expire all leases whose TTL elapsed by `nowMs` (defaults to now()). */
  expireDue(nowMs: number = this.now()): LeaseEvent[] {
    const expired: LeaseEvent[] = [];
    for (const [leaseId, lock] of this.locks) {
      if (lock.state === 'LOCKED' && lock.expiresAtMs !== undefined && lock.expiresAtMs <= nowMs) {
        const lamport = this.clock.tick();
        const expireEvent = this.trail.record({
          lamport,
          wallClockMs: nowMs,
          type: 'LEASE_EXPIRED',
          leaseId,
          causedBy: lock.grantedEventId ? [lock.grantedEventId] : [],
          details: { holder: lock.holder, version: lock.version },
        });
        this.changeLockState(leaseId, lock, 'UNLOCKED', 'ttl-expired', nowMs, [expireEvent.id]);
        lock.holder = undefined;
        lock.expiresAtMs = undefined;
        lock.grantedEventId = undefined;
        expired.push(expireEvent);
      }
    }
    return expired;
  }

  // ---------------------------------------------------------------------

  private handleAcquire(req: InboundRequest, requestEventId: string): Outcome {
    const lock = this.getOrCreateLock(req.leaseId);
    // A lock whose TTL already elapsed counts as expired at arrival time.
    if (lock.state === 'LOCKED' && lock.expiresAtMs !== undefined && lock.expiresAtMs <= req.arriveMs) {
      this.expireDue(req.arriveMs);
    }
    if (lock.state === 'LOCKED') {
      const event = this.trail.record({
        lamport: this.clock.tick(),
        wallClockMs: this.now(),
        type: 'LEASE_REJECTED',
        leaseId: req.leaseId,
        nodeId: req.nodeId,
        causedBy: [requestEventId, ...(lock.grantedEventId ? [lock.grantedEventId] : [])],
        details: {
          op: 'acquire',
          reason: 'LOCK_HELD',
          holder: lock.holder,
          holderVersion: lock.version,
          grantedEventId: lock.grantedEventId,
        },
      });
      return { ok: false, event };
    }
    const ttlMs = req.ttlMs ?? 30_000;
    lock.state = 'LOCKED';
    lock.holder = req.nodeId;
    lock.version += 1;
    lock.expiresAtMs = req.arriveMs + ttlMs;
    const event = this.trail.record({
      lamport: this.clock.tick(),
      wallClockMs: this.now(),
      type: 'LEASE_GRANTED',
      leaseId: req.leaseId,
      nodeId: req.nodeId,
      causedBy: [requestEventId],
      details: { ttlMs, version: lock.version, expiresAtMs: lock.expiresAtMs },
    });
    lock.grantedEventId = event.id;
    this.changeLockState(req.leaseId, lock, 'LOCKED', 'grant', this.now(), [event.id], 'UNLOCKED');
    return { ok: true, event };
  }

  private handleRenew(req: InboundRequest, requestEventId: string): Outcome {
    const lock = this.getOrCreateLock(req.leaseId);
    if (lock.state === 'LOCKED' && lock.holder === req.nodeId &&
        (lock.expiresAtMs === undefined || lock.expiresAtMs > req.arriveMs)) {
      const ttlMs = req.ttlMs ?? 30_000;
      lock.expiresAtMs = req.arriveMs + ttlMs;
      const event = this.trail.record({
        lamport: this.clock.tick(),
        wallClockMs: this.now(),
        type: 'LEASE_RENEWED',
        leaseId: req.leaseId,
        nodeId: req.nodeId,
        causedBy: [requestEventId, ...(lock.grantedEventId ? [lock.grantedEventId] : [])],
        details: { ttlMs, version: lock.version, expiresAtMs: lock.expiresAtMs },
      });
      return { ok: true, event };
    }
    return {
      ok: false,
      event: this.reject(req, requestEventId, lock,
        lock.state !== 'LOCKED' ? 'LEASE_NOT_HELD' : 'NOT_HOLDER'),
    };
  }

  private handleTransfer(req: InboundRequest, requestEventId: string): Outcome {
    const lock = this.getOrCreateLock(req.leaseId);
    const to = req.toNodeId;
    if (lock.state === 'LOCKED' && lock.holder === req.nodeId && to && to !== req.nodeId) {
      const from = req.nodeId;
      lock.holder = to;
      lock.version += 1;
      const event = this.trail.record({
        lamport: this.clock.tick(),
        wallClockMs: this.now(),
        type: 'LEASE_TRANSFERRED',
        leaseId: req.leaseId,
        nodeId: from,
        causedBy: [requestEventId, ...(lock.grantedEventId ? [lock.grantedEventId] : [])],
        details: { fromNode: from, toNode: to, version: lock.version },
      });
      this.changeLockState(req.leaseId, lock, 'LOCKED', 'transfer', this.now(), [event.id]);
      return { ok: true, event };
    }
    return {
      ok: false,
      event: this.reject(req, requestEventId, lock,
        lock.state !== 'LOCKED' ? 'LEASE_NOT_HELD' : !to || to === req.nodeId ? 'INVALID_TARGET' : 'NOT_HOLDER'),
    };
  }

  private handleRelease(req: InboundRequest, requestEventId: string): Outcome {
    const lock = this.getOrCreateLock(req.leaseId);
    if (lock.state === 'LOCKED' && lock.holder === req.nodeId) {
      const event = this.trail.record({
        lamport: this.clock.tick(),
        wallClockMs: this.now(),
        type: 'LEASE_RELEASED',
        leaseId: req.leaseId,
        nodeId: req.nodeId,
        causedBy: [requestEventId, ...(lock.grantedEventId ? [lock.grantedEventId] : [])],
        details: { version: lock.version },
      });
      this.changeLockState(req.leaseId, lock, 'UNLOCKED', 'release', this.now(), [event.id]);
      lock.holder = undefined;
      lock.expiresAtMs = undefined;
      lock.grantedEventId = undefined;
      return { ok: true, event };
    }
    return {
      ok: false,
      event: this.reject(req, requestEventId, lock,
        lock.state !== 'LOCKED' ? 'LEASE_NOT_HELD' : 'NOT_HOLDER'),
    };
  }

  private reject(req: InboundRequest, requestEventId: string, lock: LockRecord, reason: string): LeaseEvent {
    return this.trail.record({
      lamport: this.clock.tick(),
      wallClockMs: this.now(),
      type: 'LEASE_REJECTED',
      leaseId: req.leaseId,
      nodeId: req.nodeId,
      causedBy: [requestEventId, ...(lock.grantedEventId ? [lock.grantedEventId] : [])],
      details: {
        op: req.op,
        reason,
        holder: lock.holder,
        holderVersion: lock.version,
        grantedEventId: lock.grantedEventId,
      },
    });
  }

  private detectContention(req: InboundRequest, requestEvent: LeaseEvent): void {
    for (const prev of this.trail.byType('LEASE_REQUESTED')) {
      if (prev.id === requestEvent.id) continue;
      if (prev.leaseId !== req.leaseId || prev.details.op !== 'acquire') continue;
      const gapMs = Math.abs(prev.wallClockMs - req.arriveMs);
      if (gapMs > this.contentionWindowMs) continue;
      const pairKey = [prev.id, requestEvent.id].sort().join('|');
      if (this.contentionPairs.has(pairKey)) continue;
      this.contentionPairs.add(pairKey);
      this.trail.record({
        lamport: this.clock.tick(),
        wallClockMs: req.arriveMs,
        type: 'CONTENTION_DETECTED',
        leaseId: req.leaseId,
        causedBy: [prev.id, requestEvent.id],
        details: {
          contenders: [prev.nodeId, req.nodeId],
          gapMs,
          windowMs: this.contentionWindowMs,
          firstRequestEventId: prev.id,
          secondRequestEventId: requestEvent.id,
        },
      });
    }
  }

  private changeLockState(
    leaseId: string,
    lock: LockRecord,
    to: LockState,
    reason: string,
    atMs: number,
    causedBy: string[],
    fromOverride?: LockState,
  ): LeaseEvent {
    const from = fromOverride ?? (to === 'LOCKED' ? 'UNLOCKED' : 'LOCKED');
    lock.state = to;
    return this.trail.record({
      lamport: this.clock.tick(),
      wallClockMs: atMs,
      type: 'LOCK_STATE_CHANGED',
      leaseId,
      causedBy,
      details: { from, to, reason, holder: lock.holder, version: lock.version },
    });
  }

  private getOrCreateLock(leaseId: string): LockRecord {
    let lock = this.locks.get(leaseId);
    if (!lock) {
      lock = { state: 'UNLOCKED', version: 0 };
      this.locks.set(leaseId, lock);
    }
    return lock;
  }
}

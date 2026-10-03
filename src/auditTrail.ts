import type { LeaseEvent, LeaseEventType, NewLeaseEvent } from './events.ts';

export interface ReplayFrame {
  seq: number;
  lamport: number;
  /** Milliseconds elapsed since the first recorded event. */
  offsetMs: number;
  wallClockMs: number;
  line: string;
  event: LeaseEvent;
}

/**
 * Append-only, replayable audit trail ("black box") for lease coordination.
 *
 * The trail is the single source of truth: the arbiter and the nodes never
 * talk to each other about history, they only append events here. Every
 * event carries a Lamport timestamp and explicit causal edges (`causedBy`)
 * so the exact chain "request -> lock transition -> grant -> rejection"
 * can be reconstructed after a failure.
 */
export class AuditTrail {
  private events: LeaseEvent[] = [];
  private byId = new Map<string, LeaseEvent>();
  private seq = 0;

  /** Append an event. Ids and sequence numbers are assigned here. */
  record(event: NewLeaseEvent): LeaseEvent {
    this.seq += 1;
    const full: LeaseEvent = { ...event, id: `evt-${this.seq}`, seq: this.seq };
    for (const parent of full.causedBy) {
      if (!this.byId.has(parent)) {
        throw new Error(`causal parent ${parent} of ${full.id} is not in the trail`);
      }
    }
    this.events.push(full);
    this.byId.set(full.id, full);
    return full;
  }

  /** All events in recording (arrival) order. */
  all(): readonly LeaseEvent[] {
    return this.events;
  }

  get(id: string): LeaseEvent | undefined {
    return this.byId.get(id);
  }

  byType(type: LeaseEventType): LeaseEvent[] {
    return this.events.filter((e) => e.type === type);
  }

  byLease(leaseId: string): LeaseEvent[] {
    return this.events.filter((e) => e.leaseId === leaseId);
  }

  byNode(nodeId: string): LeaseEvent[] {
    return this.events.filter((e) => e.nodeId === nodeId);
  }

  /** Events whose wall-clock time falls inside [fromMs, toMs]. */
  inWindow(fromMs: number, toMs: number): LeaseEvent[] {
    return this.events.filter((e) => e.wallClockMs >= fromMs && e.wallClockMs <= toMs);
  }

  /**
   * Full causal ancestry of an event, ordered from the root cause down to
   * the event itself. This is the "evidence chain" used in post-mortems:
   * e.g. for a LEASE_REJECTED event it yields
   *   [loser request, winner request, lock transition, winner grant, rejection].
   */
  causalChain(eventId: string): LeaseEvent[] {
    const target = this.byId.get(eventId);
    if (!target) throw new Error(`unknown event ${eventId}`);
    const seen = new Set<string>();
    const chain: LeaseEvent[] = [];
    const visit = (e: LeaseEvent): void => {
      if (seen.has(e.id)) return;
      seen.add(e.id);
      for (const parentId of e.causedBy) {
        const parent = this.byId.get(parentId);
        if (parent) visit(parent);
      }
      chain.push(e);
    };
    visit(target);
    chain.sort((a, b) => a.lamport - b.lamport || a.seq - b.seq);
    return chain;
  }

  /**
   * True if `a` happens-before `b` according to the recorded causal edges
   * (transitively). Falls back to Lamport comparison is intentionally NOT
   * done: this answers "is there a proven causal path", nothing else.
   */
  isCausalAncestor(ancestorId: string, eventId: string): boolean {
    return this.causalChain(eventId).some((e) => e.id === ancestorId);
  }

  /**
   * Replay the whole trail as a timeline ordered by logical time
   * (Lamport, tie-broken by recorder sequence). Each frame contains a
   * human-readable line plus the underlying structured event.
   */
  replay(leaseId?: string): ReplayFrame[] {
    const source = leaseId ? this.byLease(leaseId) : [...this.events];
    const ordered = source.sort((a, b) => a.lamport - b.lamport || a.seq - b.seq);
    if (ordered.length === 0) return [];
    const t0 = ordered[0].wallClockMs;
    return ordered.map((event) => ({
      seq: event.seq,
      lamport: event.lamport,
      offsetMs: event.wallClockMs - t0,
      wallClockMs: event.wallClockMs,
      line: formatEvent(event, event.wallClockMs - t0),
      event,
    }));
  }
}

function formatEvent(e: LeaseEvent, offsetMs: number): string {
  const head = `[L#${String(e.lamport).padStart(3, ' ')} t+${offsetMs.toFixed(3).padStart(8, ' ')}ms]`;
  const node = e.nodeId ? ` ${e.nodeId}` : '';
  const d = e.details;
  switch (e.type) {
    case 'LEASE_REQUESTED':
      return `${head} REQUEST   ${node} -> arbiter op=${d.op} lease=${e.leaseId} ttl=${d.ttlMs ?? '-'}ms`;
    case 'LEASE_GRANTED':
      return `${head} GRANT     lease=${e.leaseId} holder=${e.nodeId} expires@+${d.ttlMs}ms (version ${d.version})`;
    case 'LEASE_RENEWED':
      return `${head} RENEW     lease=${e.leaseId} holder=${e.nodeId} extended to +${d.ttlMs}ms`;
    case 'LEASE_TRANSFERRED':
      return `${head} TRANSFER  lease=${e.leaseId} ${d.fromNode} -> ${d.toNode}`;
    case 'LEASE_RELEASED':
      return `${head} RELEASE   lease=${e.leaseId} by ${e.nodeId}`;
    case 'LEASE_EXPIRED':
      return `${head} EXPIRE    lease=${e.leaseId} holder=${d.holder} TTL elapsed`;
    case 'LEASE_REJECTED':
      return `${head} REJECT    ${node} op=${d.op} lease=${e.leaseId} reason=${d.reason} (lock held by ${d.holder})`;
    case 'CONTENTION_DETECTED':
      return `${head} RACE!     lease=${e.leaseId} contenders=${(d.contenders as string[]).join(' vs ')} arrival gap=${(d.gapMs as number).toFixed(3)}ms`;
    case 'LOCK_STATE_CHANGED':
      return `${head} LOCK      lease=${e.leaseId} ${d.from} -> ${d.to} (${d.reason}${d.holder ? `, holder=${d.holder}` : ''})`;
  }
}

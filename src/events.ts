/**
 * The event log is the single source of truth for replay. Every external
 * stimulus — network packet arrivals, clock-interrupt timeouts and random
 * seed injections — is recorded here in (tick, seq) order.
 */

export interface PacketPayload {
  id: number;
  from: string;
  size: number;
}

export interface TimeoutPayload {
  timerId: string;
}

export interface ReseedPayload {
  seed: number;
  reason: string;
}

export type EventPayload =
  | { kind: 'packet'; payload: PacketPayload }
  | { kind: 'timeout'; payload: TimeoutPayload }
  | { kind: 'reseed'; payload: ReseedPayload };

export type KernelEvent = EventPayload & {
  /** Virtual clock tick at which the event is dispatched. */
  tick: number;
  /** Tie-breaker for events sharing a tick; assigned at record time. */
  seq: number;
};

export type EventKind = KernelEvent['kind'];

/** Order events by tick, then by record sequence — total deterministic order. */
export function compareEvents(a: KernelEvent, b: KernelEvent): number {
  return a.tick - b.tick || a.seq - b.seq;
}

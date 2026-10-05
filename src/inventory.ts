export interface Allocation {
  batchId: string;
  qty: number;
}

/**
 * Event-sourced batch inventory ledger.
 *
 * Core rules:
 * - FEFO outbound: allocate by earliest expiry, tie-broken by inbound sequence.
 * - Reservations hold stock and can be cancelled; same-instant competition is
 *   resolved in record order (first recorded wins), never over-allocating.
 * - Late-recorded inbounds (business time in the past) never change the batch
 *   attribution of outbounds already recorded: allocation decisions are
 *   computed against the state known at record time and stored immutably on
 *   the outbound event.
 * - Expired batches (expiry earlier than the operation's business date) are
 *   quarantined: they stay on hand but are never allocated.
 * - Returns go back to their original batch and cannot exceed what was
 *   shipped from that batch.
 */

export interface BatchInfo {
  batchId: string;
  expiry: string;
  inboundSeq: number;
}

interface BaseEvent {
  recordSeq: number;
  eventTime: string;
}

export interface InboundEvent extends BaseEvent {
  kind: 'inbound';
  batchId: string;
  qty: number;
  expiry: string;
  inboundSeq: number;
}

export interface OutboundEvent extends BaseEvent {
  kind: 'outbound';
  qty: number;
  allocations: Allocation[];
  shortage: number;
}

export interface ReserveEvent extends BaseEvent {
  kind: 'reserve';
  reservationId: string;
  qty: number;
  allocations: Allocation[];
}

export interface CancelReservationEvent extends BaseEvent {
  kind: 'cancel-reservation';
  reservationId: string;
}

export interface ReturnEvent extends BaseEvent {
  kind: 'return';
  batchId: string;
  qty: number;
}

export type InventoryEvent =
  | InboundEvent
  | OutboundEvent
  | ReserveEvent
  | CancelReservationEvent
  | ReturnEvent;

export interface BatchStock {
  batchId: string;
  expiry: string;
  inboundSeq: number;
  onHand: number;
  reserved: number;
  available: number;
  expired: boolean;
}

export interface StockReport {
  asOf: string | null;
  batches: BatchStock[];
  totals: { onHand: number; reserved: number; available: number; expired: number };
}

interface BatchAccum {
  inbound: number;
  returned: number;
  shipped: number;
  reserved: number;
}

const dateOf = (eventTime: string): string => eventTime.slice(0, 10);

export class InventoryLedger {
  private readonly events: InventoryEvent[] = [];
  private readonly batches = new Map<string, BatchInfo>();
  private inboundCounter = 0;

  receive(input: { batchId: string; qty: number; expiry: string; eventTime: string }): InboundEvent {
    if (input.qty <= 0) throw new Error('inbound qty must be positive');
    if (this.batches.has(input.batchId)) throw new Error(`duplicate batch ${input.batchId}`);
    const event: InboundEvent = {
      kind: 'inbound',
      recordSeq: this.events.length,
      eventTime: input.eventTime,
      batchId: input.batchId,
      qty: input.qty,
      expiry: input.expiry,
      inboundSeq: this.inboundCounter++,
    };
    this.batches.set(input.batchId, {
      batchId: input.batchId,
      expiry: input.expiry,
      inboundSeq: event.inboundSeq,
    });
    this.events.push(event);
    return event;
  }

  ship(input: { qty: number; eventTime: string }): OutboundEvent {
    if (input.qty <= 0) throw new Error('outbound qty must be positive');
    const allocations = this.fefoAllocate(input.qty, dateOf(input.eventTime));
    const allocated = sumQty(allocations);
    const event: OutboundEvent = {
      kind: 'outbound',
      recordSeq: this.events.length,
      eventTime: input.eventTime,
      qty: input.qty,
      allocations,
      shortage: input.qty - allocated,
    };
    this.events.push(event);
    return event;
  }

  reserve(input: { reservationId: string; qty: number; eventTime: string }): ReserveEvent {
    if (input.qty <= 0) throw new Error('reservation qty must be positive');
    if (this.events.some((e) => e.kind === 'reserve' && e.reservationId === input.reservationId)) {
      throw new Error(`duplicate reservation ${input.reservationId}`);
    }
    const allocations = this.fefoAllocate(input.qty, dateOf(input.eventTime));
    const event: ReserveEvent = {
      kind: 'reserve',
      recordSeq: this.events.length,
      eventTime: input.eventTime,
      reservationId: input.reservationId,
      qty: input.qty,
      allocations,
    };
    this.events.push(event);
    return event;
  }

  cancelReservation(input: { reservationId: string; eventTime: string }): CancelReservationEvent {
    const { activeReservations } = this.replay();
    if (!activeReservations.has(input.reservationId)) {
      throw new Error(`no active reservation ${input.reservationId}`);
    }
    const event: CancelReservationEvent = {
      kind: 'cancel-reservation',
      recordSeq: this.events.length,
      eventTime: input.eventTime,
      reservationId: input.reservationId,
    };
    this.events.push(event);
    return event;
  }

  returnGoods(input: { batchId: string; qty: number; eventTime: string }): ReturnEvent {
    if (input.qty <= 0) throw new Error('return qty must be positive');
    if (!this.batches.has(input.batchId)) throw new Error(`unknown batch ${input.batchId}`);
    const { perBatch } = this.replay();
    const acc = perBatch.get(input.batchId);
    const shipped = acc?.shipped ?? 0;
    const returned = acc?.returned ?? 0;
    if (returned + input.qty > shipped) {
      throw new Error(
        `return of ${input.qty} to ${input.batchId} exceeds shipped-but-not-returned ${shipped - returned}`,
      );
    }
    const event: ReturnEvent = {
      kind: 'return',
      recordSeq: this.events.length,
      eventTime: input.eventTime,
      batchId: input.batchId,
      qty: input.qty,
    };
    this.events.push(event);
    return event;
  }

  snapshot(): StockReport {
    return this.buildReport(null);
  }

  stockAsOf(asOf: string): StockReport {
    return this.buildReport(asOf);
  }

  get log(): readonly InventoryEvent[] {
    return this.events;
  }

  private fefoAllocate(qty: number, asOfDate: string): Allocation[] {
    const { perBatch } = this.replay();
    const candidates: Array<{ info: BatchInfo; available: number }> = [];
    for (const info of this.batches.values()) {
      if (info.expiry < asOfDate) continue;
      const acc = perBatch.get(info.batchId);
      const available =
        (acc?.inbound ?? 0) + (acc?.returned ?? 0) - (acc?.shipped ?? 0) - (acc?.reserved ?? 0);
      if (available > 0) candidates.push({ info, available });
    }
    candidates.sort((a, b) =>
      a.info.expiry === b.info.expiry
        ? a.info.inboundSeq - b.info.inboundSeq
        : a.info.expiry < b.info.expiry
          ? -1
          : 1,
    );
    const allocations: Allocation[] = [];
    let remaining = qty;
    for (const { info, available } of candidates) {
      if (remaining === 0) break;
      const take = Math.min(available, remaining);
      allocations.push({ batchId: info.batchId, qty: take });
      remaining -= take;
    }
    return allocations;
  }

  private replay(asOf?: string): {
    perBatch: Map<string, BatchAccum>;
    activeReservations: Map<string, Allocation[]>;
  } {
    const perBatch = new Map<string, BatchAccum>();
    const activeReservations = new Map<string, Allocation[]>();
    const accum = (batchId: string): BatchAccum => {
      let acc = perBatch.get(batchId);
      if (!acc) {
        acc = { inbound: 0, returned: 0, shipped: 0, reserved: 0 };
        perBatch.set(batchId, acc);
      }
      return acc;
    };
    for (const event of this.events) {
      if (asOf !== undefined && event.eventTime > asOf) continue;
      switch (event.kind) {
        case 'inbound':
          accum(event.batchId).inbound += event.qty;
          break;
        case 'return':
          accum(event.batchId).returned += event.qty;
          break;
        case 'outbound':
          for (const alloc of event.allocations) accum(alloc.batchId).shipped += alloc.qty;
          break;
        case 'reserve':
          activeReservations.set(event.reservationId, event.allocations);
          for (const alloc of event.allocations) accum(alloc.batchId).reserved += alloc.qty;
          break;
        case 'cancel-reservation': {
          const held = activeReservations.get(event.reservationId);
          if (held) {
            for (const alloc of held) accum(alloc.batchId).reserved -= alloc.qty;
            activeReservations.delete(event.reservationId);
          }
          break;
        }
      }
    }
    return { perBatch, activeReservations };
  }

  private buildReport(asOf: string | null): StockReport {
    const { perBatch } = this.replay(asOf ?? undefined);
    const reportDate = asOf === null ? null : dateOf(asOf);
    const batches: BatchStock[] = [];
    for (const info of this.batches.values()) {
      const acc = perBatch.get(info.batchId);
      const onHand = (acc?.inbound ?? 0) + (acc?.returned ?? 0) - (acc?.shipped ?? 0);
      const reserved = acc?.reserved ?? 0;
      batches.push({
        batchId: info.batchId,
        expiry: info.expiry,
        inboundSeq: info.inboundSeq,
        onHand,
        reserved,
        available: onHand - reserved,
        expired: reportDate !== null && info.expiry < reportDate,
      });
    }
    batches.sort((a, b) => a.inboundSeq - b.inboundSeq);
    return {
      asOf,
      batches,
      totals: {
        onHand: batches.reduce((n, b) => n + b.onHand, 0),
        reserved: batches.reduce((n, b) => n + b.reserved, 0),
        available: batches.reduce((n, b) => n + b.available, 0),
        expired: batches.filter((b) => b.expired).reduce((n, b) => n + b.onHand, 0),
      },
    };
  }
}

function sumQty(allocations: Allocation[]): number {
  return allocations.reduce((n, a) => n + a.qty, 0);
}

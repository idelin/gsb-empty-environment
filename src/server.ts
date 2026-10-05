import type { ServerMessage, SimEvent, SubscriptionFilter, WorldState } from './types.js';
import { CursorFilterMismatchError, decodeCursor, encodeCursor, filterHash, matchesFilter } from './cursor.js';
import type { CursorPayload } from './cursor.js';
import { EventStore } from './event-store.js';

export interface ServerOptions {
  secret: string;
  snapshotChunkSize?: number;
  snapshotChunkDelayMs?: number;
}

export interface Subscription {
  close(): void;
  readonly closed: boolean;
}

export function initialWorldState(): WorldState {
  return { tick: 0, prices: {}, volume: {} };
}

export function applyEvent(state: WorldState, event: SimEvent): WorldState {
  const payload = event.payload;
  if (event.kind === 'price') {
    const symbol = String(payload.symbol);
    return { ...state, prices: { ...state.prices, [symbol]: Number(payload.price) } };
  }
  if (event.kind === 'trade') {
    const symbol = String(payload.symbol);
    const qty = Number(payload.qty);
    return { ...state, volume: { ...state.volume, [symbol]: (state.volume[symbol] ?? 0) + qty } };
  }
  if (event.kind === 'tick') {
    return { ...state, tick: Number(payload.n) };
  }
  return state;
}

export class ResumeServer {
  private readonly store: EventStore;
  private readonly secret: string;
  private readonly snapshotChunkSize: number;
  private readonly snapshotChunkDelayMs: number;
  private state: WorldState = initialWorldState();
  private epochCounter = 0;

  constructor(store: EventStore, options: ServerOptions) {
    this.store = store;
    this.secret = options.secret;
    this.snapshotChunkSize = options.snapshotChunkSize ?? 8;
    this.snapshotChunkDelayMs = options.snapshotChunkDelayMs ?? 0;
    this.store.onAppend((event) => {
      this.state = applyEvent(this.state, event);
    });
  }

  currentState(): WorldState {
    return this.state;
  }

  cursorFor(seq: number, off: number, ep: number, filter: SubscriptionFilter): string {
    const payload: CursorPayload = { v: 1, fh: filterHash(filter), seq, off, ep };
    return encodeCursor(payload, this.secret);
  }

  connect(filter: SubscriptionFilter, handler: (msg: ServerMessage) => void, cursorToken?: string): Subscription {
    const fh = filterHash(filter);
    let closed = false;
    let detachLive: (() => void) | undefined;

    const sub: Subscription = {
      get closed() { return closed; },
      close() {
        closed = true;
        if (detachLive) detachLive();
      },
    };

    const deliverEvent = (event: SimEvent, epoch: number, offset: number, replay: boolean): void => {
      handler({
        type: 'event',
        epoch,
        offset,
        replay,
        event,
        cursor: this.cursorFor(event.seq, offset + 1, epoch, filter),
      });
    };

    const startLive = (epoch: number, startOffset: number): void => {
      let offset = startOffset;
      detachLive = this.store.onAppend((event) => {
        if (closed) return;
        if (!matchesFilter(filter, event.kind)) return;
        const current = offset;
        offset += 1;
        deliverEvent(event, epoch, current, false);
      });
    };

    const catchUp = (afterSeq: number, epoch: number, startOffset: number): number => {
      let offset = startOffset;
      let seq = afterSeq;
      for (;;) {
        const batch = this.store.readFrom(seq, filter);
        if (batch.length === 0) return offset;
        for (const event of batch) {
          if (closed) return offset;
          seq = event.seq;
          const current = offset;
          offset += 1;
          deliverEvent(event, epoch, current, true);
        }
      }
    };

    const runSnapshot = async (): Promise<void> => {
      const epoch = ++this.epochCounter;
      const highSeq = this.store.head();
      const unpin = this.store.pin(highSeq + 1);
      try {
        const stateAtHigh = this.state;
        const snapshotState = await this.serializeState(stateAtHigh);
        if (closed) return;
        handler({
          type: 'snapshot',
          epoch,
          baseSeq: highSeq,
          state: snapshotState,
          cursor: this.cursorFor(highSeq, 0, epoch, filter),
        });
        const nextOffset = catchUp(highSeq, epoch, 0);
        if (!closed) startLive(epoch, nextOffset);
      } finally {
        unpin();
      }
    };

    if (cursorToken !== undefined) {
      const payload = decodeCursor(cursorToken, this.secret);
      if (payload.fh !== fh) throw new CursorFilterMismatchError();
      if (payload.seq <= this.store.head() && this.store.canReplayFrom(payload.seq)) {
        const nextOffset = catchUp(payload.seq, payload.ep, payload.off);
        if (!closed) startLive(payload.ep, nextOffset);
        return sub;
      }
    } else if (this.store.oldest() === 1) {
      const epoch = ++this.epochCounter;
      const nextOffset = catchUp(0, epoch, 0);
      if (!closed) startLive(epoch, nextOffset);
      return sub;
    }

    void runSnapshot();
    return sub;
  }

  private async serializeState(state: WorldState): Promise<WorldState> {
    const prices: Record<string, number> = {};
    const volume: Record<string, number> = {};
    let processed = 0;
    for (const [symbol, price] of Object.entries(state.prices)) {
      prices[symbol] = price;
      processed += 1;
      if (processed % this.snapshotChunkSize === 0) await this.chunkDelay();
    }
    for (const [symbol, qty] of Object.entries(state.volume)) {
      volume[symbol] = qty;
      processed += 1;
      if (processed % this.snapshotChunkSize === 0) await this.chunkDelay();
    }
    return { tick: state.tick, prices, volume };
  }

  private chunkDelay(): Promise<void> {
    if (this.snapshotChunkDelayMs <= 0) return Promise.resolve();
    return new Promise((resolve) => { setTimeout(resolve, this.snapshotChunkDelayMs); });
  }
}

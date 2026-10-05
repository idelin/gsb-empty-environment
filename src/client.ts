import type { ServerMessage, SubscriptionFilter, WorldState } from './types.js';
import { applyEvent, initialWorldState } from './server.js';
import type { ResumeServer, Subscription } from './server.js';

export interface DuplicateRecord {
  eventId: string;
  seq: number;
  offset: number;
  epoch: number;
}

export class ResumeClient {
  private readonly server: ResumeServer;
  private readonly filter: SubscriptionFilter;
  private cursor: string | undefined;
  private subscription: Subscription | undefined;
  private epoch = -1;
  private expectedOffset = 0;
  private seenIds = new Set<string>();

  state: WorldState = initialWorldState();
  snapshotCount = 0;
  lastSnapshotBaseSeq = -1;
  readonly duplicates: DuplicateRecord[] = [];
  readonly anomalies: string[] = [];
  readonly applied: string[] = [];
  readonly cursors: string[] = [];

  constructor(server: ResumeServer, filter: SubscriptionFilter) {
    this.server = server;
    this.filter = filter;
  }

  currentCursor(): string | undefined {
    return this.cursor;
  }

  connect(cursorOverride?: string): void {
    this.close();
    const token = cursorOverride !== undefined ? cursorOverride : this.cursor;
    this.subscription = this.server.connect(this.filter, (msg) => this.handle(msg), token);
  }

  close(): void {
    if (this.subscription) this.subscription.close();
    this.subscription = undefined;
  }

  private handle(msg: ServerMessage): void {
    if (msg.type === 'snapshot') {
      this.state = {
        tick: msg.state.tick,
        prices: { ...msg.state.prices },
        volume: { ...msg.state.volume },
      };
      this.epoch = msg.epoch;
      this.expectedOffset = 0;
      this.seenIds.clear();
      this.snapshotCount += 1;
      this.lastSnapshotBaseSeq = msg.baseSeq;
      this.cursor = msg.cursor;
      this.cursors.push(msg.cursor);
      return;
    }
    if (this.epoch === -1) {
      this.epoch = msg.epoch;
      this.expectedOffset = msg.offset;
    }
    if (msg.epoch !== this.epoch) {
      this.anomalies.push('epoch-mismatch:' + msg.event.id);
      return;
    }
    if (msg.offset < this.expectedOffset || this.seenIds.has(msg.event.id)) {
      this.duplicates.push({ eventId: msg.event.id, seq: msg.event.seq, offset: msg.offset, epoch: msg.epoch });
      return;
    }
    if (msg.offset > this.expectedOffset) {
      this.anomalies.push('gap:expected=' + this.expectedOffset + ',got=' + msg.offset);
      return;
    }
    this.expectedOffset += 1;
    this.seenIds.add(msg.event.id);
    this.state = applyEvent(this.state, msg.event);
    this.applied.push(msg.event.id);
    this.cursor = msg.cursor;
    this.cursors.push(msg.cursor);
  }
}

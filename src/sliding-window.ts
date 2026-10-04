export type Seq = number;

export type SlotState = 'pending' | 'acked' | 'failed';

export interface Slot<T> {
  seq: Seq;
  generation: number;
  state: SlotState;
  payload: T;
}

export type DeliveryEvent<T> =
  | { type: 'deliver'; payload: T }
  | { type: 'complete'; seq: Seq; generation: number }
  | { type: 'timeout'; seq: Seq };

export type RejectReason =
  | 'window-full'
  | 'unknown-seq'
  | 'stale-generation'
  | 'not-pending'
  | 'not-failed';

export interface StepResult<T> {
  accepted: boolean;
  reason?: RejectReason;
  seq?: Seq;
  watermark: Seq;
  redeliveries: Array<Slot<T>>;
}

export class SlidingWindowDelivery<T> {
  private readonly windowSize: number;
  private readonly slots = new Map<Seq, Slot<T>>();
  private readonly redeliveryQueue: Array<Slot<T>> = [];
  private nextSeq: Seq = 1;
  private watermark: Seq = 0;

  constructor(windowSize: number) {
    if (!Number.isInteger(windowSize) || windowSize < 1) {
      throw new Error('windowSize must be a positive integer');
    }
    this.windowSize = windowSize;
  }

  apply(event: DeliveryEvent<T>): StepResult<T> {
    switch (event.type) {
      case 'deliver':
        return this.deliver(event.payload);
      case 'complete':
        return this.complete(event.seq, event.generation);
      case 'timeout':
        return this.timeout(event.seq);
    }
  }

  getWatermark(): Seq {
    return this.watermark;
  }

  getRedeliveries(): Array<Slot<T>> {
    return this.redeliveryQueue.slice();
  }

  drainRedeliveries(): Array<Slot<T>> {
    return this.redeliveryQueue.splice(0, this.redeliveryQueue.length);
  }

  redeliver(seq: Seq): StepResult<T> {
    const slot = this.slots.get(seq);
    if (!slot) {
      return this.result(false, 'unknown-seq');
    }
    if (slot.state !== 'failed') {
      return this.result(false, 'not-failed');
    }
    slot.state = 'pending';
    slot.generation += 1;
    return this.result(true, undefined, seq);
  }

  private deliver(payload: T): StepResult<T> {
    if (this.nextSeq - 1 - this.watermark >= this.windowSize) {
      return this.result(false, 'window-full');
    }
    const seq = this.nextSeq++;
    this.slots.set(seq, { seq, generation: 1, state: 'pending', payload });
    return this.result(true, undefined, seq);
  }

  private complete(seq: Seq, generation: number): StepResult<T> {
    if (seq <= this.watermark) {
      return this.result(true, undefined, seq);
    }
    const slot = this.slots.get(seq);
    if (!slot) {
      return this.result(false, 'unknown-seq');
    }
    if (slot.generation !== generation) {
      return this.result(false, 'stale-generation');
    }
    if (slot.state !== 'pending') {
      return this.result(true, undefined, seq);
    }
    slot.state = 'acked';
    this.advanceWatermark();
    return this.result(true, undefined, seq);
  }

  private timeout(seq: Seq): StepResult<T> {
    const slot = this.slots.get(seq);
    if (!slot) {
      return this.result(false, 'unknown-seq');
    }
    if (slot.state !== 'pending') {
      return this.result(false, 'not-pending');
    }
    slot.state = 'failed';
    this.redeliveryQueue.push(slot);
    return this.result(true, undefined, seq);
  }

  private advanceWatermark(): void {
    let cursor = this.watermark + 1;
    for (;;) {
      const slot = this.slots.get(cursor);
      if (!slot || slot.state !== 'acked') {
        break;
      }
      this.slots.delete(cursor);
      this.watermark = cursor;
      cursor += 1;
    }
  }

  private result(accepted: boolean, reason?: RejectReason, seq?: Seq): StepResult<T> {
    return {
      accepted,
      reason,
      seq,
      watermark: this.watermark,
      redeliveries: this.getRedeliveries(),
    };
  }
}

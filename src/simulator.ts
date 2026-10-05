import { EventStore } from './event-store.js';
import type { SimEvent } from './types.js';

const SYMBOLS = ['AAA', 'BBB', 'CCC', 'DDD', 'EEE', 'FFF', 'GGG', 'HHH'];

export class Simulator {
  private readonly store: EventStore;
  private rngState: number;
  private tickCount = 0;

  constructor(store: EventStore, seed: number) {
    this.store = store;
    this.rngState = (seed >>> 0) || 1;
  }

  private next(): number {
    let x = this.rngState;
    x ^= x << 13;
    x ^= x >>> 17;
    x ^= x << 5;
    this.rngState = x >>> 0;
    return this.rngState / 0x100000000;
  }

  step(): SimEvent {
    this.tickCount += 1;
    const r = this.next();
    const symbol = SYMBOLS[Math.floor(this.next() * SYMBOLS.length)];
    if (r < 0.45) {
      return this.store.append('price', { symbol, price: Math.round(this.next() * 100000) / 100 }, this.tickCount);
    }
    if (r < 0.9) {
      return this.store.append('trade', { symbol, qty: 1 + Math.floor(this.next() * 50) }, this.tickCount);
    }
    return this.store.append('tick', { n: this.tickCount }, this.tickCount);
  }

  run(count: number): SimEvent[] {
    const out: SimEvent[] = [];
    for (let i = 0; i < count; i += 1) out.push(this.step());
    return out;
  }
}

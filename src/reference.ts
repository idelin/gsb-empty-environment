/**
 * 逐字节参考实现：与 Scheduler 语义完全相同的“显然正确”版本。
 * 服务循环不做任何合并优化，每次游标访问只发送 1 字节，
 * 用作交叉核对的 oracle（见 test/scheduler.test.ts）。
 */

import type {
  CategoryConfig,
  CompletionEvent,
  DropEvent,
  DuplicateEvent,
  PacketInput,
  TickLedger,
} from './scheduler.ts';

interface RefPacket {
  id: string;
  bytes: number;
  remaining: number;
}

interface RefCategoryState {
  config: CategoryConfig;
  tokens: number;
  deficit: number;
  queuedBytes: number;
  queue: RefPacket[];
}

export class ReferenceScheduler {
  private states = new Map<string, RefCategoryState>();
  private order: string[] = [];
  private cursor = 0;
  private tick = 0;
  private arrivals = new Map<number, PacketInput[]>();
  private pendingReconfigs = new Map<number, Record<string, CategoryConfig>>();
  private admitted = new Map<string, { bytes: number; category: string }>();

  readonly ledger: TickLedger[] = [];
  readonly completions: CompletionEvent[] = [];
  readonly drops: DropEvent[] = [];
  readonly duplicates: DuplicateEvent[] = [];

  constructor(configs: Record<string, CategoryConfig>) {
    for (const [name, cfg] of Object.entries(configs)) {
      this.states.set(name, {
        config: { ...cfg },
        tokens: cfg.tokenCap,
        deficit: 0,
        queuedBytes: 0,
        queue: [],
      });
      this.order.push(name);
    }
  }

  get currentTick(): number {
    return this.tick;
  }

  submit(packet: PacketInput): void {
    const list = this.arrivals.get(packet.arrivalTick);
    if (list) list.push({ ...packet });
    else this.arrivals.set(packet.arrivalTick, [{ ...packet }]);
  }

  reconfigure(atTick: number, configs: Record<string, CategoryConfig>): void {
    const merged = { ...(this.pendingReconfigs.get(atTick) ?? {}) };
    for (const [name, cfg] of Object.entries(configs)) merged[name] = { ...cfg };
    this.pendingReconfigs.set(atTick, merged);
  }

  step(budget: number): TickLedger {
    const t = this.tick;
    const drops: DropEvent[] = [];
    const duplicates: DuplicateEvent[] = [];
    const completions: CompletionEvent[] = [];

    const reconfigs = this.pendingReconfigs.get(t);
    if (reconfigs) {
      this.pendingReconfigs.delete(t);
      for (const [name, cfg] of Object.entries(reconfigs)) {
        const st = this.states.get(name);
        if (!st) {
          this.states.set(name, {
            config: { ...cfg },
            tokens: cfg.tokenCap,
            deficit: 0,
            queuedBytes: 0,
            queue: [],
          });
          this.order.push(name);
        } else {
          st.config = { ...cfg };
          st.tokens = Math.min(st.tokens, cfg.tokenCap);
          st.deficit = 0;
        }
      }
    }

    const arrived = this.arrivals.get(t);
    if (arrived) {
      this.arrivals.delete(t);
      for (const p of arrived) {
        const st = this.states.get(p.category);
        if (!st) {
          drops.push({ tick: t, id: p.id, reason: 'unknown-category' });
          continue;
        }
        const prev = this.admitted.get(p.id);
        if (prev) {
          if (prev.bytes === p.bytes && prev.category === p.category) {
            duplicates.push({ tick: t, id: p.id });
          } else {
            drops.push({ tick: t, id: p.id, reason: 'conflict' });
          }
          continue;
        }
        if (st.queuedBytes + p.bytes > st.config.maxQueueBytes) {
          drops.push({ tick: t, id: p.id, reason: 'queue-limit' });
          continue;
        }
        this.admitted.set(p.id, { bytes: p.bytes, category: p.category });
        st.queue.push({ id: p.id, bytes: p.bytes, remaining: p.bytes });
        st.queuedBytes += p.bytes;
      }
    }

    for (const name of this.order) {
      const st = this.states.get(name)!;
      st.tokens = Math.min(st.config.tokenCap, st.tokens + st.config.tokenRate);
      if (st.queue.length > 0) st.deficit += st.config.weight;
    }

    // 逐字节服务：每次访问恰好发送 1 字节
    const sentByCategory: Record<string, number> = {};
    let remaining = budget;
    const n = this.order.length;
    while (remaining > 0) {
      let target = -1;
      for (let i = 0; i < n; i++) {
        const j = (this.cursor + i) % n;
        const st = this.states.get(this.order[j])!;
        if (st.queue.length > 0 && st.tokens >= 1 && st.deficit >= 1) {
          target = j;
          break;
        }
      }
      if (target === -1) break;
      const name = this.order[target];
      const st = this.states.get(name)!;
      st.tokens -= 1;
      st.deficit -= 1;
      st.queuedBytes -= 1;
      st.queue[0].remaining -= 1;
      remaining -= 1;
      sentByCategory[name] = (sentByCategory[name] ?? 0) + 1;
      if (st.queue[0].remaining === 0) {
        const done = st.queue.shift()!;
        completions.push({ tick: t, id: done.id, category: name, bytes: done.bytes });
        if (st.queue.length === 0) st.deficit = 0;
      }
      this.cursor = (target + 1) % n;
    }

    const entry: TickLedger = {
      tick: t,
      budget,
      sentTotal: budget - remaining,
      sentByCategory,
      tokensByCategory: Object.fromEntries(
        this.order.map((name) => [name, this.states.get(name)!.tokens]),
      ),
      deficitByCategory: Object.fromEntries(
        this.order.map((name) => [name, this.states.get(name)!.deficit]),
      ),
      queuedBytesByCategory: Object.fromEntries(
        this.order.map((name) => [name, this.states.get(name)!.queuedBytes]),
      ),
      drops,
      duplicates,
      completions,
    };
    this.ledger.push(entry);
    this.completions.push(...completions);
    this.drops.push(...drops);
    this.duplicates.push(...duplicates);
    this.tick = t + 1;
    return entry;
  }
}

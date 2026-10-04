/**
 * Deliberately naive, byte-at-a-time reference simulation.
 *
 * Implements the exact same semantics as Scheduler (same tick event order,
 * same initial tokens, same deficit rules) but with the most literal
 * formulation possible: the send path moves one byte per iteration and
 * re-checks every invariant on each byte. It exists to cross-check the
 * chunked grant arithmetic of Scheduler.
 */

import {
  CLASS_ORDER,
  type ClassConfig,
  type ClassName,
  type CompletionRecord,
  type DropRecord,
  type PacketSpec,
  type TickLedger,
} from './scheduler.ts';

interface RefPacket {
  id: string;
  arrivalTick: number;
  bytes: number;
  remaining: number;
  className: ClassName;
}

interface RefClass {
  config: ClassConfig;
  tokens: number;
  deficit: number;
  queue: RefPacket[];
}

export class ReferenceScheduler {
  private readonly linkBytesPerTick: number;
  private readonly quantumUnit: number;
  private classes: Record<ClassName, RefClass>;
  private tick = 0;
  private arrivals = new Map<number, PacketSpec[]>();
  private reconfigs = new Map<number, { className: ClassName; patch: Partial<ClassConfig> }[]>();
  private seen = new Map<string, string>();
  private cursor = 0;

  constructor(
    linkBytesPerTick: number,
    classes: Record<ClassName, ClassConfig>,
    quantumUnit: number,
  ) {
    this.linkBytesPerTick = linkBytesPerTick;
    this.quantumUnit = quantumUnit;
    this.classes = {
      voice: init(classes.voice),
      interactive: init(classes.interactive),
      bulk: init(classes.bulk),
    };
  }

  submit(spec: PacketSpec): void {
    const list = this.arrivals.get(spec.arrivalTick) ?? [];
    list.push(spec);
    this.arrivals.set(spec.arrivalTick, list);
  }

  scheduleReconfig(tick: number, className: ClassName, patch: Partial<ClassConfig>): void {
    const list = this.reconfigs.get(tick) ?? [];
    list.push({ className, patch });
    this.reconfigs.set(tick, list);
  }

  step(): TickLedger {
    const tick = this.tick;
    const dropped: DropRecord[] = [];
    const completed: CompletionRecord[] = [];

    for (const req of this.reconfigs.get(tick) ?? []) {
      const cls = this.classes[req.className];
      cls.config = { ...cls.config, ...req.patch };
      if (cls.tokens > cls.config.tokenBurstBytes) cls.tokens = cls.config.tokenBurstBytes;
      cls.deficit = 0;
    }
    this.reconfigs.delete(tick);

    for (const name of CLASS_ORDER) {
      const cls = this.classes[name];
      cls.tokens = Math.min(cls.config.tokenBurstBytes, cls.tokens + cls.config.tokenRatePerTick);
    }

    for (const spec of this.arrivals.get(tick) ?? []) {
      const fp = `${spec.className}|${spec.bytes}|${spec.arrivalTick}`;
      const prev = this.seen.get(spec.id);
      if (prev !== undefined) {
        if (prev === fp) continue;
        dropped.push({
          tick,
          packetId: spec.id,
          className: spec.className,
          bytes: spec.bytes,
          reason: 'id_conflict',
        });
        continue;
      }
      const cls = this.classes[spec.className];
      const queued = cls.queue.reduce((sum, p) => sum + p.remaining, 0);
      if (queued + spec.bytes > cls.config.maxQueueBytes) {
        dropped.push({
          tick,
          packetId: spec.id,
          className: spec.className,
          bytes: spec.bytes,
          reason: 'queue_overflow',
        });
        continue;
      }
      this.seen.set(spec.id, fp);
      cls.queue.push({ ...spec, remaining: spec.bytes });
    }
    this.arrivals.delete(tick);

    const sentByClass: Record<ClassName, number> = { voice: 0, interactive: 0, bulk: 0 };
    let remaining = this.linkBytesPerTick;
    let progress = true;
    while (remaining > 0 && progress) {
      progress = false;
      for (let i = 0; i < CLASS_ORDER.length && remaining > 0; i++) {
        const name = CLASS_ORDER[(this.cursor + i) % CLASS_ORDER.length];
        const cls = this.classes[name];
        const head = cls.queue[0];
        if (head === undefined) {
          cls.deficit = 0;
          continue;
        }
        if (cls.tokens <= 0) continue;
        cls.deficit += cls.config.weight * this.quantumUnit;
        // Literal byte-at-a-time send: each byte re-checks every constraint.
        while (
          head.remaining > 0 &&
          cls.deficit > 0 &&
          cls.tokens > 0 &&
          remaining > 0
        ) {
          head.remaining -= 1;
          cls.deficit -= 1;
          cls.tokens -= 1;
          remaining -= 1;
          sentByClass[name] += 1;
          progress = true;
        }
        if (head.remaining === 0) {
          cls.queue.shift();
          completed.push({
            tick,
            packetId: head.id,
            className: head.className,
            bytes: head.bytes,
            arrivalTick: head.arrivalTick,
            latencyTicks: tick - head.arrivalTick,
          });
          if (cls.queue.length === 0) cls.deficit = 0;
        }
      }
    }
    this.cursor = (this.cursor + 1) % CLASS_ORDER.length;

    const queuedBytes = (name: ClassName): number =>
      this.classes[name].queue.reduce((sum, p) => sum + p.remaining, 0);

    const ledger: TickLedger = {
      tick,
      linkCapacity: this.linkBytesPerTick,
      totalSent: this.linkBytesPerTick - remaining,
      sentByClass,
      tokensAfter: {
        voice: this.classes.voice.tokens,
        interactive: this.classes.interactive.tokens,
        bulk: this.classes.bulk.tokens,
      },
      deficitAfter: {
        voice: this.classes.voice.deficit,
        interactive: this.classes.interactive.deficit,
        bulk: this.classes.bulk.deficit,
      },
      queuedBytesAfter: {
        voice: queuedBytes('voice'),
        interactive: queuedBytes('interactive'),
        bulk: queuedBytes('bulk'),
      },
      completed,
      dropped,
    };
    this.tick += 1;
    return ledger;
  }
}

function init(config: ClassConfig): RefClass {
  return {
    config: { ...config },
    tokens: config.tokenBurstBytes,
    deficit: 0,
    queue: [],
  };
}

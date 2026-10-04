/**
 * Integer-tick hierarchical packet scheduler for an offline network simulator.
 *
 * Per class: byte token bucket (rate/burst), max queued bytes, DRR weight.
 * Packets may be served across multiple ticks; a completion is reported once.
 *
 * Tick-internal event order (tick t), see README.md for the full rationale:
 *   1. Apply pending reconfigurations atomically.
 *   2. Replenish token buckets: tokens = min(burst, tokens + rate).
 *   3. Admit arrivals of tick t (idempotency/conflict check, then whole-packet
 *      queue-limit check).
 *   4. DRR service loop until the link budget is exhausted or no queue can
 *      make progress.
 *   5. Emit the per-tick ledger.
 *
 * Initial state: tokens = tokenBurstBytes (full bucket), deficit = 0.
 */

export type ClassName = 'voice' | 'interactive' | 'bulk';

/** Fixed visitation order inside one DRR round; the round start rotates per tick. */
export const CLASS_ORDER: readonly ClassName[] = ['voice', 'interactive', 'bulk'];

export interface ClassConfig {
  /** Token refill per tick, in bytes. */
  tokenRatePerTick: number;
  /** Token bucket capacity (max burst), in bytes. */
  tokenBurstBytes: number;
  /** Maximum queued bytes; whole-packet admission check. */
  maxQueueBytes: number;
  /** DRR weight; quantum per visit = weight * quantumUnit. */
  weight: number;
}

export interface PacketSpec {
  id: string;
  arrivalTick: number;
  bytes: number;
  className: ClassName;
}

export type DropReason = 'queue_overflow' | 'id_conflict';

export interface DropRecord {
  tick: number;
  packetId: string;
  className: ClassName;
  bytes: number;
  reason: DropReason;
}

export interface CompletionRecord {
  tick: number;
  packetId: string;
  className: ClassName;
  bytes: number;
  arrivalTick: number;
  /** completion tick - arrival tick */
  latencyTicks: number;
}

export interface TickLedger {
  tick: number;
  linkCapacity: number;
  totalSent: number;
  sentByClass: Record<ClassName, number>;
  tokensAfter: Record<ClassName, number>;
  deficitAfter: Record<ClassName, number>;
  queuedBytesAfter: Record<ClassName, number>;
  completed: CompletionRecord[];
  dropped: DropRecord[];
}

export interface ReconfigRequest {
  tick: number;
  className: ClassName;
  patch: Partial<ClassConfig>;
}

interface QueuedPacket {
  id: string;
  arrivalTick: number;
  bytes: number;
  remaining: number;
  className: ClassName;
}

interface ClassState {
  config: ClassConfig;
  tokens: number;
  deficit: number;
  queue: QueuedPacket[];
  headIndex: number;
  queuedBytes: number;
}

export interface SchedulerTotals {
  submittedPackets: number;
  submittedBytes: number;
  admittedPackets: number;
  admittedBytes: number;
  droppedPackets: number;
  droppedBytes: number;
  completedPackets: number;
  sentBytes: number;
}

function emptyClassRecord(): Record<ClassName, number> {
  return { voice: 0, interactive: 0, bulk: 0 };
}

function fingerprint(spec: PacketSpec): string {
  return `${spec.className}|${spec.bytes}|${spec.arrivalTick}`;
}

export class Scheduler {
  readonly linkBytesPerTick: number;
  readonly quantumUnit: number;

  private states: Record<ClassName, ClassState>;
  private currentTick = 0;
  private pendingArrivals = new Map<number, PacketSpec[]>();
  private pendingReconfigs = new Map<number, ReconfigRequest[]>();
  /** Packet id -> content fingerprint, recorded on admission only. */
  private seen = new Map<string, string>();
  /** Rotates the DRR round start each tick so no class is permanently first. */
  private rrCursor = 0;

  readonly totals: SchedulerTotals = {
    submittedPackets: 0,
    submittedBytes: 0,
    admittedPackets: 0,
    admittedBytes: 0,
    droppedPackets: 0,
    droppedBytes: 0,
    completedPackets: 0,
    sentBytes: 0,
  };

  constructor(
    linkBytesPerTick: number,
    classes: Record<ClassName, ClassConfig>,
    quantumUnit = 256,
  ) {
    if (!Number.isInteger(linkBytesPerTick) || linkBytesPerTick <= 0) {
      throw new Error('linkBytesPerTick must be a positive integer');
    }
    if (!Number.isInteger(quantumUnit) || quantumUnit <= 0) {
      throw new Error('quantumUnit must be a positive integer');
    }
    this.linkBytesPerTick = linkBytesPerTick;
    this.quantumUnit = quantumUnit;
    this.states = {
      voice: initState(classes.voice),
      interactive: initState(classes.interactive),
      bulk: initState(classes.bulk),
    };
  }

  get tick(): number {
    return this.currentTick;
  }

  /** Buffer a packet arrival. arrivalTick must not be in the past. */
  submit(spec: PacketSpec): void {
    if (!Number.isInteger(spec.arrivalTick) || spec.arrivalTick < this.currentTick) {
      throw new Error(
        `packet ${spec.id}: arrivalTick ${spec.arrivalTick} is before current tick ${this.currentTick}`,
      );
    }
    if (!Number.isInteger(spec.bytes) || spec.bytes <= 0) {
      throw new Error(`packet ${spec.id}: bytes must be a positive integer`);
    }
    const list = this.pendingArrivals.get(spec.arrivalTick) ?? [];
    list.push(spec);
    this.pendingArrivals.set(spec.arrivalTick, list);
  }

  /**
   * Schedule a reconfiguration that takes effect atomically at the start of
   * the given tick (step 1 of the tick, before token refill and admissions).
   * Shrinking maxQueueBytes never drops queued packets; it only rejects new
   * admissions until queuedBytes falls below the new limit.
   */
  scheduleReconfig(req: ReconfigRequest): void {
    if (!Number.isInteger(req.tick) || req.tick < this.currentTick) {
      throw new Error(`reconfig tick ${req.tick} is before current tick ${this.currentTick}`);
    }
    const list = this.pendingReconfigs.get(req.tick) ?? [];
    list.push(req);
    this.pendingReconfigs.set(req.tick, list);
  }

  /** Advance exactly one tick and return its ledger. */
  step(): TickLedger {
    const tick = this.currentTick;
    const dropped: DropRecord[] = [];
    const completed: CompletionRecord[] = [];

    // (1) Atomic reconfiguration.
    for (const req of this.pendingReconfigs.get(tick) ?? []) {
      this.applyReconfig(req);
    }
    this.pendingReconfigs.delete(tick);

    // (2) Token refill.
    for (const name of CLASS_ORDER) {
      const st = this.states[name];
      st.tokens = Math.min(st.config.tokenBurstBytes, st.tokens + st.config.tokenRatePerTick);
    }

    // (3) Admissions for this tick, in submission order.
    for (const spec of this.pendingArrivals.get(tick) ?? []) {
      this.admit(tick, spec, dropped);
    }
    this.pendingArrivals.delete(tick);

    // (4) DRR service loop.
    const sentByClass = emptyClassRecord();
    let remaining = this.linkBytesPerTick;
    while (remaining > 0) {
      let progress = false;
      for (let i = 0; i < CLASS_ORDER.length && remaining > 0; i++) {
        const name = CLASS_ORDER[(this.rrCursor + i) % CLASS_ORDER.length];
        const st = this.states[name];
        const head = peekHead(st);
        if (head === undefined) {
          st.deficit = 0; // DRR: an empty queue forfeits its deficit.
          continue;
        }
        if (st.tokens <= 0) {
          continue; // no tokens: skip without accumulating deficit
        }
        st.deficit += st.config.weight * this.quantumUnit;
        const grant = Math.min(st.deficit, st.tokens, remaining, head.remaining);
        if (grant <= 0) continue;
        this.applyGrant(st, head, grant);
        sentByClass[name] += grant;
        remaining -= grant;
        progress = true;
        if (head.remaining === 0) {
          dequeueHead(st);
          completed.push({
            tick,
            packetId: head.id,
            className: head.className,
            bytes: head.bytes,
            arrivalTick: head.arrivalTick,
            latencyTicks: tick - head.arrivalTick,
          });
          this.totals.completedPackets += 1;
          if (peekHead(st) === undefined) st.deficit = 0;
        }
      }
      if (!progress) break;
    }
    this.rrCursor = (this.rrCursor + 1) % CLASS_ORDER.length;

    // (5) Ledger.
    const ledger: TickLedger = {
      tick,
      linkCapacity: this.linkBytesPerTick,
      totalSent: this.linkBytesPerTick - remaining,
      sentByClass,
      tokensAfter: { ...mapValues(this.states, (s) => s.tokens) },
      deficitAfter: { ...mapValues(this.states, (s) => s.deficit) },
      queuedBytesAfter: { ...mapValues(this.states, (s) => s.queuedBytes) },
      completed,
      dropped,
    };
    this.currentTick += 1;
    return ledger;
  }

  /** Run n ticks, returning every ledger. */
  run(ticks: number): TickLedger[] {
    const out: TickLedger[] = [];
    for (let i = 0; i < ticks; i++) out.push(this.step());
    return out;
  }

  /** Run until all queues are empty and no arrivals remain (or maxTicks). */
  runUntilIdle(maxTicks: number): TickLedger[] {
    const out: TickLedger[] = [];
    for (let i = 0; i < maxTicks; i++) {
      out.push(this.step());
      if (this.isIdle()) break;
    }
    return out;
  }

  isIdle(): boolean {
    if (this.pendingArrivals.size > 0) return false;
    return CLASS_ORDER.every((name) => this.states[name].queuedBytes === 0);
  }

  queuedBytes(className: ClassName): number {
    return this.states[className].queuedBytes;
  }

  /** Chunked send of `grant` bytes from the head packet. */
  protected applyGrant(st: ClassState, head: QueuedPacket, grant: number): void {
    head.remaining -= grant;
    st.tokens -= grant;
    st.deficit -= grant;
    st.queuedBytes -= grant;
    this.totals.sentBytes += grant;
  }

  private applyReconfig(req: ReconfigRequest): void {
    const st = this.states[req.className];
    st.config = { ...st.config, ...req.patch };
    if (st.tokens > st.config.tokenBurstBytes) st.tokens = st.config.tokenBurstBytes;
    // Weights/quantum changed: the old deficit is void.
    st.deficit = 0;
    // Note: maxQueueBytes shrink keeps every queued packet (no drops here).
  }

  private admit(tick: number, spec: PacketSpec, dropped: DropRecord[]): void {
    this.totals.submittedPackets += 1;
    this.totals.submittedBytes += spec.bytes;
    const fp = fingerprint(spec);
    const prev = this.seen.get(spec.id);
    if (prev !== undefined) {
      if (prev === fp) return; // idempotent duplicate: ignore silently
      dropped.push({
        tick,
        packetId: spec.id,
        className: spec.className,
        bytes: spec.bytes,
        reason: 'id_conflict',
      });
      this.totals.droppedPackets += 1;
      this.totals.droppedBytes += spec.bytes;
      return;
    }
    const st = this.states[spec.className];
    if (st.queuedBytes + spec.bytes > st.config.maxQueueBytes) {
      dropped.push({
        tick,
        packetId: spec.id,
        className: spec.className,
        bytes: spec.bytes,
        reason: 'queue_overflow',
      });
      this.totals.droppedPackets += 1;
      this.totals.droppedBytes += spec.bytes;
      return;
    }
    this.seen.set(spec.id, fp);
    st.queue.push({
      id: spec.id,
      arrivalTick: spec.arrivalTick,
      bytes: spec.bytes,
      remaining: spec.bytes,
      className: spec.className,
    });
    st.queuedBytes += spec.bytes;
    this.totals.admittedPackets += 1;
    this.totals.admittedBytes += spec.bytes;
  }
}

function initState(config: ClassConfig): ClassState {
  for (const [key, value] of Object.entries(config)) {
    if (!Number.isInteger(value) || value < 0) {
      throw new Error(`invalid ClassConfig.${key}: ${value}`);
    }
  }
  return {
    config: { ...config },
    tokens: config.tokenBurstBytes, // start with a full bucket
    deficit: 0,
    queue: [],
    headIndex: 0,
    queuedBytes: 0,
  };
}

function peekHead(st: ClassState): QueuedPacket | undefined {
  return st.headIndex < st.queue.length ? st.queue[st.headIndex] : undefined;
}

function dequeueHead(st: ClassState): void {
  st.headIndex += 1;
  if (st.headIndex >= 64 && st.headIndex * 2 >= st.queue.length) {
    st.queue = st.queue.slice(st.headIndex);
    st.headIndex = 0;
  }
}

function mapValues(
  states: Record<ClassName, ClassState>,
  fn: (s: ClassState) => number,
): Record<ClassName, number> {
  return {
    voice: fn(states.voice),
    interactive: fn(states.interactive),
    bulk: fn(states.bulk),
  };
}

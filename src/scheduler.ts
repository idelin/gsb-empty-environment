/**
 * tick-drr-scheduler
 *
 * 整数 tick 驱动的分级发包器。
 * 每个类别拥有：字节令牌桶（tokenRate/tokenCap）、最大排队字节（maxQueueBytes）、
 * DRR 权重（weight，即每 tick 累积的亏欠字节数）。
 *
 * tick 内事件顺序（step(budget) 处理 tick t 时严格按此顺序）：
 *   1. 原子应用所有安排在 tick t 的类别重配置；
 *   2. 处理 arrivalTick === t 的到达包（按提交顺序）：去重 / 冲突 / 队限检查；
 *   3. 令牌补充：tokens = min(tokenCap, tokens + tokenRate)；
 *   4. 亏欠累积：对每个非空队列 deficit += weight（空队列 deficit 恒为 0）；
 *   5. 服务：持久游标轮转，直到额度耗尽或没有可服务队列；
 *   6. 记录本 tick 账本。
 *
 * 初始令牌：构造（或新增类别）时 tokens = tokenCap（满桶）。
 * 亏欠规则：仅非空队列累积；队列排空时 deficit 归零；重配置时 deficit 归零、
 *           tokens 截断到新 tokenCap。
 */

export interface CategoryConfig {
  /** 每 tick 补充的令牌字节数（>= 0 的整数） */
  tokenRate: number;
  /** 令牌桶容量（字节，>= 1 的整数） */
  tokenCap: number;
  /** 该类最大排队字节数（>= 0 的整数） */
  maxQueueBytes: number;
  /** DRR 权重：非空队列每 tick 累积的亏欠字节数（>= 1 的整数） */
  weight: number;
}

export interface PacketInput {
  id: string;
  arrivalTick: number;
  bytes: number;
  category: string;
}

export type DropReason = 'queue-limit' | 'conflict' | 'unknown-category';

export interface DropEvent {
  tick: number;
  id: string;
  reason: DropReason;
}

export interface DuplicateEvent {
  tick: number;
  id: string;
}

export interface CompletionEvent {
  tick: number;
  id: string;
  category: string;
  bytes: number;
}

export interface TickLedger {
  tick: number;
  budget: number;
  sentTotal: number;
  sentByCategory: Record<string, number>;
  tokensByCategory: Record<string, number>;
  deficitByCategory: Record<string, number>;
  queuedBytesByCategory: Record<string, number>;
  drops: DropEvent[];
  duplicates: DuplicateEvent[];
  completions: CompletionEvent[];
}

interface Packet {
  id: string;
  bytes: number;
  remaining: number;
}

interface CategoryState {
  config: CategoryConfig;
  tokens: number;
  deficit: number;
  queuedBytes: number;
  queue: Packet[];
}

function assertNonNegInt(value: number, what: string): void {
  if (!Number.isInteger(value) || value < 0) {
    throw new Error(`${what} must be a non-negative integer, got ${value}`);
  }
}

function validateConfig(cfg: CategoryConfig, name: string): void {
  assertNonNegInt(cfg.tokenRate, `${name}.tokenRate`);
  if (!Number.isInteger(cfg.tokenCap) || cfg.tokenCap < 1) {
    throw new Error(`${name}.tokenCap must be an integer >= 1, got ${cfg.tokenCap}`);
  }
  assertNonNegInt(cfg.maxQueueBytes, `${name}.maxQueueBytes`);
  if (!Number.isInteger(cfg.weight) || cfg.weight < 1) {
    throw new Error(`${name}.weight must be an integer >= 1, got ${cfg.weight}`);
  }
}

export class Scheduler {
  private states = new Map<string, CategoryState>();
  private order: string[] = [];
  private cursor = 0;
  private tick = 0;
  private arrivals = new Map<number, PacketInput[]>();
  private pendingReconfigs = new Map<number, Record<string, CategoryConfig>>();
  /** 仅记录“已被接纳入队”的包 ID，用于幂等去重与冲突检测 */
  private admitted = new Map<string, { bytes: number; category: string }>();

  readonly ledger: TickLedger[] = [];
  readonly completions: CompletionEvent[] = [];
  readonly drops: DropEvent[] = [];
  readonly duplicates: DuplicateEvent[] = [];

  constructor(configs: Record<string, CategoryConfig>) {
    const names = Object.keys(configs);
    if (names.length === 0) throw new Error('at least one category is required');
    for (const name of names) {
      validateConfig(configs[name], name);
      this.states.set(name, {
        config: { ...configs[name] },
        tokens: configs[name].tokenCap, // 初始令牌 = 满桶
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

  /** 登记一个到达包；arrivalTick 不得早于当前 tick。 */
  submit(packet: PacketInput): void {
    if (!Number.isInteger(packet.arrivalTick) || packet.arrivalTick < this.tick) {
      throw new Error(`arrivalTick must be an integer >= current tick (${this.tick})`);
    }
    if (!Number.isInteger(packet.bytes) || packet.bytes < 1) {
      throw new Error(`bytes must be an integer >= 1, got ${packet.bytes}`);
    }
    const list = this.arrivals.get(packet.arrivalTick);
    if (list) list.push({ ...packet });
    else this.arrivals.set(packet.arrivalTick, [{ ...packet }]);
  }

  /** 登记类别重配置，在 atTick 开始时原子生效。 */
  reconfigure(atTick: number, configs: Record<string, CategoryConfig>): void {
    if (!Number.isInteger(atTick) || atTick < this.tick) {
      throw new Error(`reconfigure tick must be an integer >= current tick (${this.tick})`);
    }
    for (const name of Object.keys(configs)) validateConfig(configs[name], name);
    const merged = { ...(this.pendingReconfigs.get(atTick) ?? {}) };
    for (const [name, cfg] of Object.entries(configs)) merged[name] = { ...cfg };
    this.pendingReconfigs.set(atTick, merged);
  }

  /** 推进一个 tick，budget 为本 tick 链路可发送字节额度，返回本 tick 账本。 */
  step(budget: number): TickLedger {
    assertNonNegInt(budget, 'budget');
    const t = this.tick;
    const drops: DropEvent[] = [];
    const duplicates: DuplicateEvent[] = [];
    const completions: CompletionEvent[] = [];

    // 1. 原子应用重配置
    const reconfigs = this.pendingReconfigs.get(t);
    if (reconfigs) {
      this.pendingReconfigs.delete(t);
      for (const [name, cfg] of Object.entries(reconfigs)) this.applyConfig(name, cfg);
    }

    // 2. 到达入队（提交顺序）
    const arrived = this.arrivals.get(t);
    if (arrived) {
      this.arrivals.delete(t);
      for (const p of arrived) this.enqueue(t, p, drops, duplicates);
    }

    // 3. 令牌补充；4. 亏欠累积
    for (const name of this.order) {
      const st = this.states.get(name)!;
      st.tokens = Math.min(st.config.tokenCap, st.tokens + st.config.tokenRate);
      if (st.queue.length > 0) st.deficit += st.config.weight;
    }

    // 5. 服务：持久游标轮转。字节级参考语义见 reference.ts；
    //    这里的优化：当游标扫到的可服务队列是唯一可服务队列时，
    //    一次性发送 min(deficit, tokens, 队首剩余, 剩余额度) 字节
    //    （发送只改变本队列状态，其他队列的可服务性在本 tick 服务阶段不会
    //    由“不可服务”变“可服务”，故与逐字节发送完全等价）。
    const sentByCategory: Record<string, number> = {};
    let remaining = budget;
    const n = this.order.length;
    while (remaining > 0) {
      let first = -1;
      for (let i = 0; i < n; i++) {
        const j = (this.cursor + i) % n;
        if (this.eligible(j)) { first = j; break; }
      }
      if (first === -1) break;
      let alone = true;
      for (let i = 1; i < n; i++) {
        const j = (first + i) % n;
        if (this.eligible(j)) { alone = false; break; }
      }
      const name = this.order[first];
      const st = this.states.get(name)!;
      let grant = 1;
      if (alone) {
        grant = Math.min(st.deficit, st.tokens, st.queue[0].remaining, remaining);
      }
      st.tokens -= grant;
      st.deficit -= grant;
      st.queuedBytes -= grant;
      st.queue[0].remaining -= grant;
      remaining -= grant;
      sentByCategory[name] = (sentByCategory[name] ?? 0) + grant;
      if (st.queue[0].remaining === 0) {
        const done = st.queue.shift()!;
        completions.push({ tick: t, id: done.id, category: name, bytes: done.bytes });
        if (st.queue.length === 0) st.deficit = 0;
      }
      this.cursor = (first + 1) % n;
    }

    // 6. 账本
    const entry: TickLedger = {
      tick: t,
      budget,
      sentTotal: budget - remaining,
      sentByCategory,
      tokensByCategory: this.snapshot((s) => s.tokens),
      deficitByCategory: this.snapshot((s) => s.deficit),
      queuedBytesByCategory: this.snapshot((s) => s.queuedBytes),
      drops,
      duplicates,
      completions,
    };
    if (entry.sentTotal > budget) throw new Error('invariant violated: sentTotal > budget');
    this.ledger.push(entry);
    this.completions.push(...completions);
    this.drops.push(...drops);
    this.duplicates.push(...duplicates);
    this.tick = t + 1;
    return entry;
  }

  private eligible(index: number): boolean {
    const st = this.states.get(this.order[index])!;
    return st.queue.length > 0 && st.tokens >= 1 && st.deficit >= 1;
  }

  private snapshot(pick: (s: CategoryState) => number): Record<string, number> {
    const out: Record<string, number> = {};
    for (const name of this.order) out[name] = pick(this.states.get(name)!);
    return out;
  }

  private applyConfig(name: string, cfg: CategoryConfig): void {
    const st = this.states.get(name);
    if (!st) {
      // 新类别：满桶启动，追加到轮转末尾
      this.states.set(name, {
        config: { ...cfg },
        tokens: cfg.tokenCap,
        deficit: 0,
        queuedBytes: 0,
        queue: [],
      });
      this.order.push(name);
      return;
    }
    // 既有类别：队列内容保留（缩容不丢既有包）；令牌截断到新桶容；亏欠归零
    st.config = { ...cfg };
    st.tokens = Math.min(st.tokens, cfg.tokenCap);
    st.deficit = 0;
  }

  private enqueue(
    t: number,
    p: PacketInput,
    drops: DropEvent[],
    duplicates: DuplicateEvent[],
  ): void {
    const st = this.states.get(p.category);
    if (!st) {
      drops.push({ tick: t, id: p.id, reason: 'unknown-category' });
      return;
    }
    const prev = this.admitted.get(p.id);
    if (prev) {
      if (prev.bytes === p.bytes && prev.category === p.category) {
        duplicates.push({ tick: t, id: p.id }); // 内容一致：幂等，无状态变化
      } else {
        drops.push({ tick: t, id: p.id, reason: 'conflict' }); // 内容不同：冲突，保留原包
      }
      return;
    }
    // 整包入队前判断超限：queuedBytes + bytes > maxQueueBytes 则整包拒绝
    if (st.queuedBytes + p.bytes > st.config.maxQueueBytes) {
      drops.push({ tick: t, id: p.id, reason: 'queue-limit' });
      return;
    }
    this.admitted.set(p.id, { bytes: p.bytes, category: p.category });
    st.queue.push({ id: p.id, bytes: p.bytes, remaining: p.bytes });
    st.queuedBytes += p.bytes;
  }
}

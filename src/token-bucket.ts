/**
 * CAS-Based Token Bucket —— 原子无锁并发令牌桶
 *
 * 设计：
 * - 全部共享状态打包进一个 64 位原子字（SharedArrayBuffer + Atomics）：
 *     [ 距创建毫秒数 lastMs : 34 bit ][ 定点令牌数 tokensFp : 30 bit ]
 *   每次"刷新时间 + 扣减令牌"通过一次 Atomics.compareExchange 原子提交，
 *   不存在任何 Mutex / 排他锁，CAS 失败仅自旋重试，无死锁、无脏数据。
 * - 令牌采用定点数（1 token = 1024 fp），补充量按
 *     entitlement(t) = floor(elapsedMs(t) * refillRate * 1024 / 1000)
 *   的差值计算（伸缩求和，telescoping），因此任意时刻满足精确守恒：
 *     已放行(fp) + 剩余(fp) === 初始(fp) + entitlement(lastMs)
 *   （前提：桶未触顶饱和；饱和时超出容量部分按语义被丢弃，退化为 <=）。
 * - 跨线程：创建方 new 出实例后把 `buffer` 传给 Worker，Worker 以相同
 *   参数 + buffer 附加到同一共享状态，即可实现百万级并发安全。
 *
 * 约束：capacity <= MAX_CAPACITY（约 104 万 token），创建后约 544 年内有效。
 */

const TIME_BITS = 34n;
const TOKEN_BITS = 30n;
const TIME_SHIFT = TOKEN_BITS;
const TIME_MASK = (1n << TIME_BITS) - 1n;
const TOKEN_MASK = (1n << TOKEN_BITS) - 1n;

/** 定点精度：1 token = 1024 个 fp 单位 */
export const TOKEN_SCALE = 1024;
/** 容量上限（受 30 bit 定点字段约束） */
export const MAX_CAPACITY = Math.floor(Number(TOKEN_MASK) / TOKEN_SCALE);

const STATE_BYTES = 16; // [0..7] packed u64 state, [8..15] f64 createdAtMs

export interface TokenBucketOptions {
  /** 桶容量（token） */
  capacity: number;
  /** 补充速率（token/秒） */
  refillRate: number;
  /** 初始令牌数，默认等于 capacity */
  initialTokens?: number;
  /** 传入则附加到既有共享状态（用于 Worker 线程），否则创建新状态 */
  buffer?: SharedArrayBuffer;
}

export interface BucketSnapshot {
  /** 状态字中记录的最后刷新时间（距创建毫秒数） */
  elapsedMs: number;
  /** 状态字中剩余的定点令牌数 */
  tokensFp: number;
  /** 剩余令牌（token 单位） */
  tokens: number;
  /** 截至 elapsedMs 理论已铸造总量（初始 + 应补充），定点单位 */
  mintedFp: number;
}

export class CASBasedTokenBucket {
  readonly capacity: number;
  readonly refillRate: number;
  readonly buffer: SharedArrayBuffer;

  private readonly word: BigUint64Array;
  private readonly createdAt: Float64Array;
  private readonly capacityFp: number;
  private readonly initialFp: number;
  private readonly refillFpPerSec: bigint;

  constructor(options: TokenBucketOptions) {
    const { capacity, refillRate, buffer } = options;
    const initialTokens = options.initialTokens ?? capacity;

    if (!Number.isFinite(capacity) || capacity <= 0 || capacity > MAX_CAPACITY) {
      throw new RangeError(`capacity must be in (0, ${MAX_CAPACITY}]`);
    }
    if (!Number.isFinite(refillRate) || refillRate < 0) {
      throw new RangeError('refillRate must be a non-negative finite number');
    }
    if (!Number.isFinite(initialTokens) || initialTokens < 0 || initialTokens > capacity) {
      throw new RangeError('initialTokens must be in [0, capacity]');
    }
    if (buffer !== undefined && buffer.byteLength < STATE_BYTES) {
      throw new RangeError(`buffer must be at least ${STATE_BYTES} bytes`);
    }

    this.capacity = capacity;
    this.refillRate = refillRate;
    this.buffer = buffer ?? new SharedArrayBuffer(STATE_BYTES);
    this.word = new BigUint64Array(this.buffer, 0, 1);
    this.createdAt = new Float64Array(this.buffer, 8, 1);
    this.capacityFp = Math.round(capacity * TOKEN_SCALE);
    this.initialFp = Math.round(initialTokens * TOKEN_SCALE);
    this.refillFpPerSec = BigInt(Math.round(refillRate * TOKEN_SCALE));

    if (buffer === undefined) {
      // 仅创建方初始化共享状态；附加方（Worker）直接使用
      Atomics.store(this.word, 0, (0n << TIME_SHIFT) | BigInt(this.initialFp));
      this.createdAt[0] = Date.now();
    }
  }

  /** 无锁尝试获取 tokens 个令牌；成功返回 true，不足返回 false（不扣减） */
  tryAcquire(tokens = 1): boolean {
    const cost = tokens * TOKEN_SCALE;
    if (!Number.isInteger(cost) || cost <= 0) {
      throw new RangeError(`tokens must be a positive multiple of 1/${TOKEN_SCALE}`);
    }
    if (cost > this.capacityFp) return false;

    for (;;) {
      const word = Atomics.load(this.word, 0);
      const lastMs = Number(word >> TIME_SHIFT);
      const storedFp = Number(word & TOKEN_MASK);

      // 单调时钟：时钟回拨时不倒退
      let nowMs = this.nowElapsedMs();
      if (nowMs < lastMs) nowMs = lastMs;

      const refill = this.entitlementFp(nowMs) - this.entitlementFp(lastMs);
      let avail = storedFp + Number(refill);
      if (avail > this.capacityFp) avail = this.capacityFp; // 触顶丢弃（令牌桶语义）

      if (avail < cost) return false; // 不足：不写回，直接失败，降低争用

      const next = (BigInt(nowMs) << TIME_SHIFT) | (BigInt(avail - cost) & TOKEN_MASK);
      if (Atomics.compareExchange(this.word, 0, word, next) === word) return true;
      // CAS 冲突：状态字未被污染，自旋重试
    }
  }

  /** 异步等待直到获取成功或超时（内部仍是 CAS 自旋 + 让出事件循环） */
  async acquire(tokens = 1, timeoutMs = Infinity): Promise<boolean> {
    const deadline = Date.now() + timeoutMs;
    while (!this.tryAcquire(tokens)) {
      if (Date.now() >= deadline) return false;
      await new Promise((resolve) => setTimeout(resolve, 1));
    }
    return true;
  }

  /** 只读快照：当前可用令牌（含按时间应补充部分），不写共享状态 */
  availableTokens(): number {
    const word = Atomics.load(this.word, 0);
    const lastMs = Number(word >> TIME_SHIFT);
    const storedFp = Number(word & TOKEN_MASK);
    let nowMs = this.nowElapsedMs();
    if (nowMs < lastMs) nowMs = lastMs;
    const refill = this.entitlementFp(nowMs) - this.entitlementFp(lastMs);
    return Math.min(this.capacityFp, storedFp + Number(refill)) / TOKEN_SCALE;
  }

  /** 只读一致性快照：用于校验 已放行 + 剩余 === 已铸造 的守恒恒等式 */
  inspect(): BucketSnapshot {
    const word = Atomics.load(this.word, 0);
    const elapsedMs = Number(word >> TIME_SHIFT);
    const tokensFp = Number(word & TOKEN_MASK);
    const mintedFp = this.initialFp + Number(this.entitlementFp(elapsedMs));
    return { elapsedMs, tokensFp, tokens: tokensFp / TOKEN_SCALE, mintedFp };
  }

  private nowElapsedMs(): number {
    const elapsed = Date.now() - this.createdAt[0];
    if (elapsed < 0) return 0;
    const max = Number(TIME_MASK);
    return elapsed > max ? max : elapsed;
  }

  /** 自创建起经过 elapsedMs 毫秒时，理论应补充的定点令牌总量（向下取整） */
  private entitlementFp(elapsedMs: number): bigint {
    return (BigInt(Math.floor(elapsedMs)) * this.refillFpPerSec) / 1000n;
  }
}

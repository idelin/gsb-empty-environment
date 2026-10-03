/**
 * CAS-Based Token Bucket —— 原子无锁并发令牌桶
 *
 * 核心思想:
 *   把「最后刷新时间(32bit 毫秒)」与「可用令牌数(32bit 定点数)」打包进
 *   同一个 64 位原子变量, 用 Atomics.compareExchange 一次性提交两个字段,
 *   全程没有任何互斥锁 (Mutex / Lock), 多线程 / 多 Worker 下无死锁、无脏数据。
 *
 * 共享内存布局 (64 字节, 独占一条缓存行):
 *   byte 0..8   BigUint64  packedState = (lastRefillMs << 32) | availableScaled
 *   byte 8..16  Float64    epochMs (初始化时的 Date.now(), 只写一次)
 *
 * 令牌用定点数表示: SCALE = 1024 (10 bit 小数), 因此容量上限 ≈ 419 万令牌。
 * 补给量按「绝对时间的确定函数」计算: refill(t) = floor(t * rateScaledPerSec / 1000),
 * 多次 CAS 重试 / 多次刷新之间不存在累计舍入误差, 总量可被精确验证。
 */

export const SCALE = 1024n;
export const MAX_CAPACITY = 4_194_303; // floor((2^32 - 1) / 1024)
export const BUFFER_BYTES = 64;

const STATE_OFFSET = 0; // BigUint64Array 下标 0
const EPOCH_BYTE_OFFSET = 8;

export interface BucketOptions {
  /** 桶容量(令牌数), 上限 MAX_CAPACITY */
  capacity: number;
  /** 每秒补给速率(令牌数/秒) */
  refillRatePerSec: number;
  /** 初始令牌数, 默认等于 capacity */
  initialTokens?: number;
  /** 传入则接入已有共享桶(用于跨 Worker 协作); 不传则新建 SharedArrayBuffer */
  buffer?: SharedArrayBuffer;
  /** 可注入时钟(测试用), 默认 Date.now */
  now?: () => number;
}

export interface BucketSnapshot {
  lastRefillMs: number;
  availableScaled: bigint;
}

/** 补给量的绝对时间函数: elapsedMs 毫秒内累计补给的令牌(定点数) */
export function scaledElapsedTokens(elapsedMs: number, rateScaledPerSec: bigint): bigint {
  return (BigInt(elapsedMs) * rateScaledPerSec) / 1000n;
}

export class CasTokenBucket {
  readonly capacity: number;
  readonly refillRatePerSec: number;
  readonly buffer: SharedArrayBuffer;

  private readonly state: BigUint64Array;
  private readonly epochView: Float64Array;
  private readonly capacityScaled: bigint;
  private readonly rateScaledPerSec: bigint;
  private readonly now: () => number;

  constructor(options: BucketOptions) {
    const { capacity, refillRatePerSec, buffer } = options;
    if (!Number.isInteger(capacity) || capacity <= 0 || capacity > MAX_CAPACITY) {
      throw new RangeError(`capacity 必须是 1..${MAX_CAPACITY} 的整数`);
    }
    if (!(refillRatePerSec > 0) || !Number.isFinite(refillRatePerSec)) {
      throw new RangeError('refillRatePerSec 必须是正数');
    }
    this.capacity = capacity;
    this.refillRatePerSec = refillRatePerSec;
    this.capacityScaled = BigInt(capacity) * SCALE;
    this.rateScaledPerSec = BigInt(Math.round(refillRatePerSec * Number(SCALE)));
    this.now = options.now ?? Date.now;

    this.buffer = buffer ?? new SharedArrayBuffer(BUFFER_BYTES);
    if (this.buffer.byteLength < BUFFER_BYTES) {
      throw new RangeError(`buffer 至少需要 ${BUFFER_BYTES} 字节`);
    }
    this.state = new BigUint64Array(this.buffer, 0, 1);
    this.epochView = new Float64Array(this.buffer, EPOCH_BYTE_OFFSET, 1);

    if (!buffer) {
      // 创建者: 初始化共享状态 (单线程阶段, 直接写即可)
      const initial = options.initialTokens ?? capacity;
      if (!(initial >= 0) || initial > capacity) {
        throw new RangeError('initialTokens 必须在 0..capacity 之间');
      }
      this.epochView[0] = this.now();
      Atomics.store(this.state, STATE_OFFSET, CasTokenBucket.pack(0, BigInt(Math.floor(initial)) * SCALE));
    }
  }

  private static pack(lastRefillMs: number, availableScaled: bigint): bigint {
    return (BigInt(lastRefillMs) << 32n) | availableScaled;
  }

  private static unpackTime(packed: bigint): number {
    return Number(packed >> 32n);
  }

  private static unpackTokens(packed: bigint): bigint {
    return packed & 0xffffffffn;
  }

  private elapsedScaled(elapsedMs: number): bigint {
    return scaledElapsedTokens(elapsedMs, this.rateScaledPerSec);
  }

  private nowMs(): number {
    return Math.max(0, Math.floor(this.now() - this.epochView[0]));
  }

  /**
   * 无锁尝试获取 tokens 个令牌。
   * 成功返回 true; 令牌不足返回 false (并尽力把已结算的补给写回)。
   * 全程仅使用 Atomics.compareExchange, 无任何互斥锁。
   */
  tryAcquire(tokens = 1): boolean {
    if (!Number.isInteger(tokens) || tokens < 0 || tokens > this.capacity) {
      throw new RangeError('tokens 必须是 0..capacity 的整数');
    }
    const cost = BigInt(tokens) * SCALE;
    for (;;) {
      const current = Atomics.load(this.state, STATE_OFFSET);
      const lastMs = CasTokenBucket.unpackTime(current);
      const avail = CasTokenBucket.unpackTokens(current);

      const nowMs = this.nowMs();
      const elapsed = nowMs - lastMs;
      let next = avail;
      let nextMs = lastMs;
      if (elapsed > 0) {
        // 绝对时间函数求差 -> 多次刷新无累计舍入误差
        const refilled = this.elapsedScaled(nowMs) - this.elapsedScaled(lastMs);
        next = avail + refilled;
        if (next > this.capacityScaled) next = this.capacityScaled;
        nextMs = nowMs;
      }

      if (next < cost) {
        // 令牌不足: 尽力提交补给结算, 避免时间漂移, 然后失败返回
        if (elapsed > 0) {
          Atomics.compareExchange(this.state, STATE_OFFSET, current, CasTokenBucket.pack(nextMs, next));
        }
        return false;
      }

      const desired = CasTokenBucket.pack(nextMs, next - cost);
      if (Atomics.compareExchange(this.state, STATE_OFFSET, current, desired) === current) {
        return true; // CAS 成功: 时间与令牌数被原子地一起提交
      }
      // CAS 失败说明有竞争者抢先提交, 重读最新状态重试 —— 无锁、无死锁
    }
  }

  /** 阻塞式获取: 自旋重试直到成功 (演示用; 生产可换成退避/挂起策略) */
  async acquire(tokens = 1): Promise<void> {
    while (!this.tryAcquire(tokens)) {
      await new Promise((resolve) => setImmediate(resolve));
    }
  }

  /** 当前可用令牌(含按当前时间结算的补给, 不写回) */
  availableTokens(): number {
    const current = Atomics.load(this.state, STATE_OFFSET);
    const lastMs = CasTokenBucket.unpackTime(current);
    const avail = CasTokenBucket.unpackTokens(current);
    const nowMs = this.nowMs();
    let next = avail;
    if (nowMs > lastMs) {
      next = avail + (this.elapsedScaled(nowMs) - this.elapsedScaled(lastMs));
      if (next > this.capacityScaled) next = this.capacityScaled;
    }
    return Number(next) / Number(SCALE);
  }

  /** 结算到当前时间并返回打包状态快照 (测试对账用) */
  snapshot(): BucketSnapshot {
    this.tryAcquire(0); // 0 令牌获取 = 强制把补给结算写回
    const current = Atomics.load(this.state, STATE_OFFSET);
    return {
      lastRefillMs: CasTokenBucket.unpackTime(current),
      availableScaled: CasTokenBucket.unpackTokens(current),
    };
  }
}

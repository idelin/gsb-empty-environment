/**
 * Shared Storage selectURL 选择预算。
 *
 * 防止追踪方通过反复调用 selectURL 逐步泄露跨站标识：
 * 从 n 个候选中选择 1 个会泄露 log2(n) 比特，每个（顶层站点, worklet 源）
 * 在 24 小时滑动窗口内最多泄露 12 比特。余额不足时返回第 0 个候选且不扣费。
 */

export const MIN_CANDIDATES = 2;
export const MAX_CANDIDATES = 8;
export const DEFAULT_WINDOW_MS = 24 * 60 * 60 * 1000;
export const DEFAULT_MAX_BITS = 12;

export interface SelectionBudgetOptions {
  /** 滑动窗口长度（毫秒），默认 24 小时。 */
  windowMs?: number;
  /** 窗口内比特预算上限，默认 12。 */
  maxBits?: number;
  /** 时钟（注入便于测试），默认 Date.now。 */
  now?: () => number;
}

/** 选择器：给定候选数 n，返回 [0, n) 内的候选下标。 */
export type Chooser = (numCandidates: number) => number;

interface Charge {
  /** 到期时刻（毫秒）。now >= expiresAt 时该笔扣费失效。 */
  readonly expiresAt: number;
  /** 该次选择的候选数 n，费用为 log2(n) 比特。 */
  readonly candidates: number;
}

function assertValidCandidateCount(numCandidates: number): void {
  if (
    typeof numCandidates !== "number" ||
    !Number.isInteger(numCandidates) ||
    numCandidates < MIN_CANDIDATES ||
    numCandidates > MAX_CANDIDATES
  ) {
    throw new RangeError(
      `候选数必须是 ${MIN_CANDIDATES} 到 ${MAX_CANDIDATES} 之间的整数，收到: ${String(numCandidates)}`,
    );
  }
}

function defaultChooser(numCandidates: number): number {
  return Math.floor(Math.random() * numCandidates);
}

export class SelectionBudget {
  private readonly windowMs: number;
  /** 2^maxBits：用乘积代替 log2 累加，彻底规避浮点误差。 */
  private readonly maxProduct: number;
  private readonly now: () => number;
  private readonly charges = new Map<string, Charge[]>();

  constructor(options: SelectionBudgetOptions = {}) {
    const { windowMs = DEFAULT_WINDOW_MS, maxBits = DEFAULT_MAX_BITS, now = Date.now } = options;
    if (!Number.isFinite(windowMs) || windowMs <= 0) {
      throw new RangeError(`windowMs 必须是正数，收到: ${String(windowMs)}`);
    }
    if (!Number.isInteger(maxBits) || maxBits <= 0) {
      throw new RangeError(`maxBits 必须是正整数，收到: ${String(maxBits)}`);
    }
    this.windowMs = windowMs;
    this.maxProduct = 2 ** maxBits;
    this.now = now;
  }

  /**
   * 尝试在预算内做一次选择，返回候选下标。
   *
   * 余额不足时返回 0 且不扣费（也不会调用 choose）。
   * 整个“检查余额 + 扣费”是同步原子的，因此并发调用天然按到达顺序结算。
   */
  select(
    topLevelSite: string,
    workletOrigin: string,
    numCandidates: number,
    choose: Chooser = defaultChooser,
  ): number {
    assertValidCandidateCount(numCandidates);
    const key = JSON.stringify([topLevelSite, workletOrigin]);
    const now = this.now();
    const active = this.prune(key, now);

    // sum(log2(ni)) + log2(n) <= maxBits  <=>  n * product(ni) <= 2^maxBits。
    // 候选数与乘积都是小整数，double 可精确表示，无浮点误差。
    let product = numCandidates;
    for (const charge of active) product *= charge.candidates;
    if (product > this.maxProduct) {
      return 0;
    }

    const index = choose(numCandidates);
    if (!Number.isInteger(index) || index < 0 || index >= numCandidates) {
      throw new RangeError(
        `choose 必须返回 [0, ${numCandidates}) 内的整数，收到: ${String(index)}`,
      );
    }

    active.push({ expiresAt: now + this.windowMs, candidates: numCandidates });
    this.charges.set(key, active);
    return index;
  }

  /** 该（顶层站点, worklet 源）当前窗口内剩余的比特数。 */
  remainingBits(topLevelSite: string, workletOrigin: string): number {
    const key = JSON.stringify([topLevelSite, workletOrigin]);
    const active = this.prune(key, this.now());
    let product = 1;
    for (const charge of active) product *= charge.candidates;
    return Math.log2(this.maxProduct / product);
  }

  /** 剔除已到期（now >= expiresAt）的扣费记录，返回仍生效的记录。 */
  private prune(key: string, now: number): Charge[] {
    const existing = this.charges.get(key);
    if (existing === undefined) return [];
    const active = existing.filter((charge) => charge.expiresAt > now);
    if (active.length === 0) this.charges.delete(key);
    else if (active.length !== existing.length) this.charges.set(key, active);
    return active;
  }
}

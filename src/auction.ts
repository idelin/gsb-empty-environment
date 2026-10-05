/**
 * 组合拍卖求解器。
 *
 * 输入若干针对物品组合的报价，同一投标方的多个报价互斥（XOR），
 * 求收入最大的无重叠中标集合；收入并列时按中标投标编号排序后的
 * 字典序取最小者。同时为每个中标者计算“移除该中标者后的最优收入”，
 * 供 VCG 结算使用。
 */

/** 单个组合报价。 */
export interface Bid {
  /** 投标编号，全局唯一。 */
  id: string;
  /** 投标方标识；同一投标方的多个报价互斥。 */
  bidder: string;
  /** 物品组合，非空且不含重复物品。 */
  items: string[];
  /** 报价金额，有限且非负。 */
  price: number;
}

/** 单个中标者的结算信息。 */
export interface Settlement {
  /** 中标投标编号。 */
  bidId: string;
  /** 中标投标方。 */
  bidder: string;
  /** 中标报价金额。 */
  price: number;
  /** 移除该中标者（其全部报价）后的最优收入。 */
  revenueWithoutWinner: number;
  /** VCG 支付：revenueWithoutWinner - (revenue - price)。 */
  vcgPayment: number;
}

/** 求解结果。 */
export interface AuctionResult {
  /** 最优总收入。 */
  revenue: number;
  /** 中标报价，按投标编号升序。 */
  winners: Bid[];
  /** 每个中标者的结算信息，顺序与 winners 一致。 */
  settlements: Settlement[];
}

/** 输入校验失败时抛出。 */
export class AuctionValidationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "AuctionValidationError";
  }
}

/** 报价数量上限。 */
export const MAX_BIDS = 25;

/** 校验报价列表，非法输入抛出 AuctionValidationError。 */
export function validateBids(bids: readonly Bid[]): void {
  if (!Array.isArray(bids)) {
    throw new AuctionValidationError("bids must be an array");
  }
  if (bids.length > MAX_BIDS) {
    throw new AuctionValidationError(
      `too many bids: ${bids.length} exceeds the limit of ${MAX_BIDS}`,
    );
  }
  const seenIds = new Set<string>();
  for (const bid of bids) {
    if (bid === null || typeof bid !== "object") {
      throw new AuctionValidationError("each bid must be an object");
    }
    if (typeof bid.id !== "string" || bid.id.length === 0) {
      throw new AuctionValidationError("each bid must have a non-empty string id");
    }
    if (seenIds.has(bid.id)) {
      throw new AuctionValidationError(`duplicate bid id: ${bid.id}`);
    }
    seenIds.add(bid.id);
    if (typeof bid.bidder !== "string" || bid.bidder.length === 0) {
      throw new AuctionValidationError(`bid ${bid.id}: bidder must be a non-empty string`);
    }
    if (!Array.isArray(bid.items) || bid.items.length === 0) {
      throw new AuctionValidationError(`bid ${bid.id}: items must be a non-empty array`);
    }
    const seenItems = new Set<string>();
    for (const item of bid.items) {
      if (typeof item !== "string" || item.length === 0) {
        throw new AuctionValidationError(`bid ${bid.id}: items must be non-empty strings`);
      }
      if (seenItems.has(item)) {
        throw new AuctionValidationError(
          `bid ${bid.id}: duplicate item in bundle: ${item}`,
        );
      }
      seenItems.add(item);
    }
    if (typeof bid.price !== "number" || !Number.isFinite(bid.price) || bid.price < 0) {
      throw new AuctionValidationError(
        `bid ${bid.id}: price must be a finite non-negative number`,
      );
    }
  }
}

interface Optimal {
  revenue: number;
  /** 中标投标编号，升序。 */
  ids: string[];
}

/** 比较两个升序编号列表的字典序，a 较小返回负数。 */
function compareIdLists(a: readonly string[], b: readonly string[]): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i += 1) {
    const x = a[i] as string;
    const y = b[i] as string;
    if (x < y) return -1;
    if (x > y) return 1;
  }
  return a.length - b.length;
}

/**
 * 分支定界求最优中标集合。
 * 主目标：收入最大；次目标：中标编号升序列表的字典序最小。
 */
function findOptimal(bids: readonly Bid[]): Optimal {
  // 按价格降序（编号升序兜底）排列，使上界尽快收紧。
  const ordered = [...bids].sort((a, b) => {
    if (b.price !== a.price) return b.price - a.price;
    return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
  });
  const suffixSum = new Float64Array(ordered.length + 1);
  for (let i = ordered.length - 1; i >= 0; i -= 1) {
    suffixSum[i] = (suffixSum[i + 1] as number) + (ordered[i] as Bid).price;
  }

  let best: Optimal = { revenue: 0, ids: [] };
  const usedItems = new Set<string>();
  const usedBidders = new Set<string>();
  const chosen: string[] = [];

  const isBetter = (revenue: number, ids: string[]): boolean => {
    if (revenue !== best.revenue) return revenue > best.revenue;
    return compareIdLists(ids, best.ids) < 0;
  };

  const dfs = (index: number, revenue: number): void => {
    // 上界：当前收入加上剩余全部报价的价格；严格小于最优时才可剪枝，
    // 相等时仍可能产生字典序更优的解。
    if (revenue + (suffixSum[index] as number) < best.revenue) return;
    if (index === ordered.length) {
      const ids = [...chosen].sort();
      if (isBetter(revenue, ids)) {
        best = { revenue, ids };
      }
      return;
    }
    const bid = ordered[index] as Bid;
    // 尝试选中：物品不重叠且投标方未中标。
    if (!usedBidders.has(bid.bidder) && bid.items.every((item) => !usedItems.has(item))) {
      usedBidders.add(bid.bidder);
      for (const item of bid.items) usedItems.add(item);
      chosen.push(bid.id);
      dfs(index + 1, revenue + bid.price);
      chosen.pop();
      for (const item of bid.items) usedItems.delete(item);
      usedBidders.delete(bid.bidder);
    }
    // 不选中。
    dfs(index + 1, revenue);
  };

  dfs(0, 0);
  return best;
}

/**
 * 求解组合拍卖。
 *
 * 返回收入最大的无重叠中标集合（同一投标方至多中一个报价），
 * 收入并列时取中标编号字典序最小者；并对每个中标者计算移除其
 * 全部报价后的最优收入与 VCG 支付。
 */
export function solveAuction(bids: readonly Bid[]): AuctionResult {
  validateBids(bids);
  const best = findOptimal(bids);
  const byId = new Map(bids.map((bid) => [bid.id, bid]));
  const winners = best.ids.map((id) => byId.get(id) as Bid);
  const settlements = winners.map((winner) => {
    // 同一投标方的报价互斥，结算时移除该投标方的全部报价。
    const remaining = bids.filter((bid) => bid.bidder !== winner.bidder);
    const without = findOptimal(remaining);
    return {
      bidId: winner.id,
      bidder: winner.bidder,
      price: winner.price,
      revenueWithoutWinner: without.revenue,
      vcgPayment: without.revenue - (best.revenue - winner.price),
    };
  });
  return { revenue: best.revenue, winners, settlements };
}

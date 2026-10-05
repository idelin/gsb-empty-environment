export const MAX_BIDS = 25;

export interface Bid {
  id: number;
  bidder: string;
  items: string[];
  price: number;
}

export interface Settlement {
  bid: Bid;
  revenueWithoutBidder: number;
  othersRevenue: number;
  vcgPayment: number;
}

export interface AuctionResult {
  revenue: number;
  winningBids: Bid[];
  settlements: Settlement[];
}

export class AuctionInputError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "AuctionInputError";
  }
}

interface Solution {
  revenue: number;
  ids: number[];
}

/**
 * 校验报价列表：
 * - 报价数量不得超过 MAX_BIDS（25）
 * - 投标编号必须为整数且不得重复
 * - 投标方必须为非空字符串
 * - 物品组合不得为空，且同一报价内物品不得重复
 * - 报价金额必须为有限非负数
 */
export function validateBids(bids: Bid[]): void {
  if (!Array.isArray(bids)) {
    throw new AuctionInputError("报价列表必须是数组");
  }
  if (bids.length > MAX_BIDS) {
    throw new AuctionInputError(`报价数量 ${bids.length} 超过上限 ${MAX_BIDS}`);
  }
  const seenIds = new Set<number>();
  for (const bid of bids) {
    if (!Number.isInteger(bid.id)) {
      throw new AuctionInputError(`投标编号 ${String(bid.id)} 不是整数`);
    }
    if (seenIds.has(bid.id)) {
      throw new AuctionInputError(`投标编号 ${bid.id} 重复`);
    }
    seenIds.add(bid.id);
    if (typeof bid.bidder !== "string" || bid.bidder.length === 0) {
      throw new AuctionInputError(`投标 ${bid.id} 的投标方不能为空`);
    }
    if (!Array.isArray(bid.items) || bid.items.length === 0) {
      throw new AuctionInputError(`投标 ${bid.id} 的物品组合为空`);
    }
    const seenItems = new Set<string>();
    for (const item of bid.items) {
      if (typeof item !== "string" || item.length === 0) {
        throw new AuctionInputError(`投标 ${bid.id} 包含非法物品名称`);
      }
      if (seenItems.has(item)) {
        throw new AuctionInputError(`投标 ${bid.id} 包含重复物品 "${item}"`);
      }
      seenItems.add(item);
    }
    if (typeof bid.price !== "number" || !Number.isFinite(bid.price) || bid.price < 0) {
      throw new AuctionInputError(`投标 ${bid.id} 的报价金额必须是非负有限数`);
    }
  }
}

function isLexSmaller(candidate: number[], incumbent: number[]): boolean {
  const a = [...candidate].sort((x, y) => x - y);
  const b = [...incumbent].sort((x, y) => x - y);
  const len = Math.min(a.length, b.length);
  for (let i = 0; i < len; i++) {
    if (a[i] !== b[i]) return a[i] < b[i];
  }
  return a.length < b.length;
}

/**
 * 分支定界求解胜者判定问题：
 * 主目标为总收入最大；收入相同时，按中标投标编号升序序列的字典序取最小者。
 * 同一投标方的多个报价互斥，且中标报价两两不得共享物品。
 */
function solveInternal(bids: Bid[]): Solution {
  const n = bids.length;
  if (n === 0) return { revenue: 0, ids: [] };

  const order = [...bids].sort((p, q) => q.price - p.price || p.id - q.id);

  const itemMasks = new Map<string, number>();
  const bidderMasks = new Map<string, number>();
  order.forEach((bid, pos) => {
    for (const item of bid.items) {
      itemMasks.set(item, (itemMasks.get(item) ?? 0) | (1 << pos));
    }
    bidderMasks.set(bid.bidder, (bidderMasks.get(bid.bidder) ?? 0) | (1 << pos));
  });

  const conflicts = order.map((bid, pos) => {
    let mask = 0;
    for (const item of bid.items) mask |= itemMasks.get(item) as number;
    mask |= bidderMasks.get(bid.bidder) as number;
    return mask & ~(1 << pos);
  });

  const suffixSum = new Array<number>(n + 1).fill(0);
  for (let pos = n - 1; pos >= 0; pos--) {
    suffixSum[pos] = suffixSum[pos + 1] + order[pos].price;
  }

  let best: Solution = { revenue: -1, ids: [] };
  const chosenIds: number[] = [];

  function dfs(pos: number, blocked: number, revenue: number): void {
    if (revenue + suffixSum[pos] < best.revenue) return;
    if (pos === n) {
      if (revenue > best.revenue || (revenue === best.revenue && isLexSmaller(chosenIds, best.ids))) {
        best = { revenue, ids: [...chosenIds] };
      }
      return;
    }
    if ((blocked & (1 << pos)) === 0) {
      chosenIds.push(order[pos].id);
      dfs(pos + 1, blocked | conflicts[pos], revenue + order[pos].price);
      chosenIds.pop();
    }
    dfs(pos + 1, blocked, revenue);
  }

  dfs(0, 0, 0);
  best.ids.sort((x, y) => x - y);
  return best;
}

/**
 * 求解组合拍卖：
 * 1. 求收入最大的无重叠中标集合（同一投标方报价互斥），次目标按投标编号字典序；
 * 2. 对每个中标者，移除其全部报价后重新求解最优收入，用于 VCG 结算：
 *    vcgPayment = revenueWithoutBidder - (revenue - 该中标报价金额)。
 */
export function solveAuction(bids: Bid[]): AuctionResult {
  validateBids(bids);

  const { revenue, ids } = solveInternal(bids);
  const winningBids = bids
    .filter((bid) => ids.includes(bid.id))
    .sort((a, b) => a.id - b.id);

  const settlements: Settlement[] = winningBids.map((bid) => {
    const remaining = bids.filter((other) => other.bidder !== bid.bidder);
    const without = solveInternal(remaining);
    const othersRevenue = revenue - bid.price;
    return {
      bid,
      revenueWithoutBidder: without.revenue,
      othersRevenue,
      vcgPayment: without.revenue - othersRevenue,
    };
  });

  return { revenue, winningBids, settlements };
}

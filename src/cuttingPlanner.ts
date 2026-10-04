/**
 * 切割规划器（一维下料 / Cutting Stock）
 *
 * 建模规则：
 * - 原料棒有多种「长度 + 单价」，采购数量不限；
 * - 每切一刀消耗固定锯缝 kerf，锯缝从可用长度中扣除，绝不可计入余料；
 * - 一根棒上切出 k 件：若最后恰好用尽（余料为 0），最后一件免切，
 *   即切割次数 = k - 1；否则切割次数 = k；
 * - 棒上余料只要还够长，可继续用于后续订单件（搜索中自然体现）；
 * - 目标：先最小化采购总成本，再最小化总切割次数（字典序）。
 */

export interface StockType {
  /** 原料棒长度 */
  length: number;
  /** 单根采购价 */
  price: number;
}

export interface Demand {
  /** 订单件长度 */
  length: number;
  /** 需求件数 */
  quantity: number;
}

export interface Problem {
  /** 固定锯缝宽度 */
  kerf: number;
  /** 可采购的原料棒规格（不限量） */
  stocks: StockType[];
  /** 小批需求 */
  demands: Demand[];
  /** 余料达到该长度视为「可复用」，默认取最短订单件长度 */
  reusableMinLength?: number;
}

export interface PlacedPiece {
  /** 订单编号（按输入顺序，从 1 开始） */
  orderId: number;
  length: number;
}

export type LeftoverUse = 'reusable' | 'scrap' | 'none';

export interface BarPlan {
  stockLength: number;
  price: number;
  /** 该棒上依次切下的订单件 */
  pieces: PlacedPiece[];
  /** 该棒的切割次数 */
  cuts: number;
  /** 余料长度（不含锯缝） */
  leftover: number;
  /** 余料去向：可复用留存 / 废料 / 无剩余 */
  leftoverUse: LeftoverUse;
}

export interface Plan {
  totalCost: number;
  totalCuts: number;
  totalPieces: number;
  bars: BarPlan[];
}

/** 需求总件数上限 */
export const MAX_TOTAL_PIECES = 22;

const EPS = 1e-9;

interface OpenBar {
  stockIndex: number;
  /** 当前剩余可用长度（已扣除已放件与锯缝） */
  remaining: number;
  cuts: number;
  pieces: PlacedPiece[];
}

/**
 * 尝试把长度为 len 的订单件放进剩余 remaining 的空间。
 * 返回放置后的剩余长度与新增切割次数；放不下返回 null。
 */
function tryPlace(
  remaining: number,
  len: number,
  kerf: number,
): { remaining: number; addedCuts: number } | null {
  if (len > remaining + EPS) return null;
  if (remaining - len <= EPS) {
    // 恰好用尽：最后一件无需下刀，余料为 0
    return { remaining: 0, addedCuts: 0 };
  }
  if (len + kerf <= remaining + EPS) {
    // 正常下刀：锯缝从可用长度中扣除
    return { remaining: remaining - len - kerf, addedCuts: 1 };
  }
  // 剩余空隙不足一道锯缝，无法下刀
  return null;
}

/** 同长度原料棒只保留最低价；完全重复的规格去重 */
function dedupeStocks(stocks: StockType[]): StockType[] {
  const byLength = new Map<number, number>();
  for (const s of stocks) {
    const prev = byLength.get(s.length);
    if (prev === undefined || s.price < prev) byLength.set(s.length, s.price);
  }
  return [...byLength.entries()]
    .map(([length, price]) => ({ length, price }))
    .sort((a, b) => b.length - a.length);
}

export function solveCuttingProblem(problem: Problem): Plan {
  const { kerf } = problem;
  if (!Number.isFinite(kerf) || kerf < 0) {
    throw new Error('锯缝 kerf 必须是非负有限数');
  }
  if (!problem.stocks?.length) {
    throw new Error('至少需要一种原料棒规格');
  }
  for (const s of problem.stocks) {
    if (!(s.length > 0) || !(s.price >= 0)) {
      throw new Error('原料棒长度必须为正数、价格不能为负');
    }
  }

  // 展开需求为单件列表
  const pieces: PlacedPiece[] = [];
  (problem.demands ?? []).forEach((d, i) => {
    if (!(d.length > 0)) throw new Error(`订单 #${i + 1} 的长度必须为正数`);
    if (!Number.isInteger(d.quantity) || d.quantity < 0) {
      throw new Error(`订单 #${i + 1} 的件数必须是非负整数`);
    }
    for (let k = 0; k < d.quantity; k++) {
      pieces.push({ orderId: i + 1, length: d.length });
    }
  });
  if (pieces.length === 0) throw new Error('需求为空，无可规划件');
  if (pieces.length > MAX_TOTAL_PIECES) {
    throw new Error(`需求总件数 ${pieces.length} 超过上限 ${MAX_TOTAL_PIECES}`);
  }

  const stocks = dedupeStocks(problem.stocks);
  const maxStock = stocks[0].length;
  for (const p of pieces) {
    if (p.length > maxStock + EPS) {
      throw new Error(`订单件长度 ${p.length} 超过最长原料棒 ${maxStock}，问题无解`);
    }
  }

  // 需求件降序排列，先难后易，利于剪枝
  pieces.sort((a, b) => b.length - a.length || a.orderId - b.orderId);

  const minUnitPrice = Math.min(...stocks.map((s) => s.price / s.length));
  const cheapestBarFitting = (len: number): number => {
    let bestPrice = Infinity;
    for (const s of stocks) {
      if (s.length + EPS >= len && s.price < bestPrice) bestPrice = s.price;
    }
    return bestPrice;
  };

  const openBars: OpenBar[] = [];
  let best: { cost: number; cuts: number; bars: OpenBar[] } | null = null;

  /**
   * 还需采购成本的下界（用于剪枝）：
   * 材料长度下界 与 新棒数量下界 取较大者。
   */
  const lowerBoundCost = (idx: number): number => {
    let restLen = 0;
    let maxLen = 0;
    let minLen = Infinity;
    for (let i = idx; i < pieces.length; i++) {
      restLen += pieces[i].length;
      if (pieces[i].length > maxLen) maxLen = pieces[i].length;
      if (pieces[i].length < minLen) minLen = pieces[i].length;
    }
    let openCap = 0;
    let fitsOpen = false;
    for (const b of openBars) {
      openCap += b.remaining;
      if (b.remaining + EPS >= maxLen) fitsOpen = true;
    }
    const extra = Math.max(0, restLen - openCap);
    const materialBound = extra * minUnitPrice;
    // 忽略锯缝估算至少还需几根新棒（宽松估计，是合法下界）
    const newBars = Math.ceil(extra / maxStock - EPS);
    let countBound = 0;
    if (newBars > 0) {
      const cheapestAny = cheapestBarFitting(minLen);
      countBound = fitsOpen
        ? newBars * cheapestAny
        : cheapestBarFitting(maxLen) + (newBars - 1) * cheapestAny;
    } else if (!fitsOpen) {
      countBound = cheapestBarFitting(maxLen);
    }
    return Math.max(materialBound, countBound);
  };

  /**
   * 状态记忆：同一「已放件数 + 各棒剩余容量多重集」下，
   * 若已有不劣（成本与切割次数都不大）的到达方式，则剪枝。
   */
  const seen = new Map<string, { cost: number; cuts: number }[]>();

  const dfs = (idx: number, cost: number, cuts: number): void => {
    if (best && (cost > best.cost || (cost === best.cost && cuts >= best.cuts))) return;
    if (idx === pieces.length) {
      best = {
        cost,
        cuts,
        bars: openBars.map((b) => ({ ...b, pieces: [...b.pieces] })),
      };
      return;
    }
    if (best && cost + lowerBoundCost(idx) > best.cost + EPS) return;

    const key =
      idx +
      '|' +
      openBars
        .map((b) => b.remaining)
        .sort((a, b) => a - b)
        .join(',');
    const entries = seen.get(key);
    if (entries) {
      for (const e of entries) {
        if (e.cost <= cost && e.cuts <= cuts) return;
      }
      entries.push({ cost, cuts });
    } else {
      seen.set(key, [{ cost, cuts }]);
    }

    const piece = pieces[idx];

    // 分支一：放进已有棒的余料（余料继续用于订单；相同剩余容量只试一次以去对称）
    const triedRemaining = new Set<number>();
    for (const bar of openBars) {
      if (triedRemaining.has(bar.remaining)) continue;
      const placed = tryPlace(bar.remaining, piece.length, kerf);
      if (!placed) continue;
      triedRemaining.add(bar.remaining);
      const prevRemaining = bar.remaining;
      bar.remaining = placed.remaining;
      bar.cuts += placed.addedCuts;
      bar.pieces.push(piece);
      dfs(idx + 1, cost, cuts + placed.addedCuts);
      bar.pieces.pop();
      bar.cuts -= placed.addedCuts;
      bar.remaining = prevRemaining;
    }

    // 分支二：新购一根原料棒
    for (let s = 0; s < stocks.length; s++) {
      const placed = tryPlace(stocks[s].length, piece.length, kerf);
      if (!placed) continue;
      openBars.push({
        stockIndex: s,
        remaining: placed.remaining,
        cuts: placed.addedCuts,
        pieces: [piece],
      });
      dfs(idx + 1, cost + stocks[s].price, cuts + placed.addedCuts);
      openBars.pop();
    }
  };

  dfs(0, 0, 0);
  if (!best) throw new Error('无可行方案');

  const reusableMin =
    problem.reusableMinLength ?? Math.min(...pieces.map((p) => p.length));

  const bars: BarPlan[] = best.bars.map((b) => {
    const leftover = b.remaining < EPS ? 0 : b.remaining;
    const leftoverUse: LeftoverUse =
      leftover === 0 ? 'none' : leftover + EPS >= reusableMin ? 'reusable' : 'scrap';
    return {
      stockLength: stocks[b.stockIndex].length,
      price: stocks[b.stockIndex].price,
      pieces: b.pieces,
      cuts: b.cuts,
      leftover,
      leftoverUse,
    };
  });

  return {
    totalCost: best.cost,
    totalCuts: best.cuts,
    totalPieces: pieces.length,
    bars,
  };
}

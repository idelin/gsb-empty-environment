/**
 * 切割规划器（一维下料 / Cutting Stock）
 *
 * 模型假设：
 * - 每种原料棒无限供应，按根计价；
 * - 锯缝宽度固定为 kerf，每锯一次消耗 kerf 长度，锯缝不计入可复用余料；
 * - 从一根棒上切 k 段订单件：若还有剩余余料，需要 k 刀（余料是第 k+1 段）；
 *   若恰好切完（余料为 0），只需 k-1 刀；
 * - 够长的余料（>= 最短订单件）可留作后续订单使用。
 *
 * 关于余料复用的关键观察（保证搜索完备性的同时简化状态）：
 *   任何“先产生余料 R、再从 R 上切集合 T”的方案，都可以把 T 直接并回产生 R 的
 *   那根棒的切割组合中：设原棒切集合 S（k 件，k 刀），余料 R = L - sum(S) - k*kerf。
 *   从 R 切 T 可行 <=> sum(T) + |T|*kerf <= R（有余料）或 sum(T)+(|T|-1)*kerf = R（恰好），
 *   两种情形都等价于 S∪T 在原棒上可行，且总刀数 k+|T|（或恰好时 k+|T|-1）不变，
 *   采购根数也不变。链式复用同理归纳。因此“显式使用余料”永远不会得到更优解，
 *   最优解一定可以表示成若干根新棒上的独立切割组合。于是只需对订单子集做 DP，
 *   余料在输出时标记去向（留存可复用 / 废料）即可。
 *
 * 目标：字典序最小化 (总采购成本, 总切割刀数, 使用根数)。
 * 需求总件数 n <= 22，状态数 2^n。
 */

export interface StockType {
  /** 原料棒长度 */
  length: number;
  /** 每根价格 */
  price: number;
}

export interface Demand {
  /** 订单件长度 */
  length: number;
  /** 需求件数 */
  count: number;
}

export interface PlannerInput {
  /** 固定锯缝宽度 */
  kerf: number;
  stocks: StockType[];
  demands: Demand[];
}

export interface BarPlan {
  /** 使用的原料棒 */
  stock: StockType;
  /** 从该棒切下的订单件长度（降序） */
  pieces: number[];
  /** 该棒所需锯切刀数 */
  cuts: number;
  /** 余料长度（已扣除锯缝，纯可复用长度） */
  remnant: number;
  /** 余料去向：reusable=留存可复用，scrap=废料 */
  remnantFate: 'reusable' | 'scrap';
}

export interface PlanResult {
  feasible: boolean;
  /** 不可行时的原因 */
  reason?: string;
  totalCost: number;
  totalCuts: number;
  totalBars: number;
  bars: BarPlan[];
  /** 判定余料是否可复用的阈值（最短订单件长度） */
  minUsefulRemnant: number;
}

const EPS = 1e-9;
export const MAX_PIECES = 22;

/** 计算在长度 L 的棒上切割集合（sum 总长, k 件）的可行性与刀数、余料。 */
function fitOnBar(
  sum: number,
  k: number,
  L: number,
  kerf: number,
): { cuts: number; remnant: number } | null {
  // k-1 刀得到 k 段的前提是恰好分完（此时最后一段无需再锯，优先取更省刀的方案）；
  // 否则需要第 k 刀把最后一段与余料分开。
  const exact = sum + (k - 1) * kerf; // k-1 刀恰好分完
  if (Math.abs(L - exact) <= EPS) {
    return { cuts: k - 1, remnant: 0 };
  }
  const withKerfAll = sum + k * kerf; // k 刀时的总消耗
  if (withKerfAll <= L + EPS) {
    return { cuts: k, remnant: Math.max(0, L - withKerfAll) };
  }
  return null;
}

export function solveCuttingStock(input: PlannerInput): PlanResult {
  const { kerf } = input;
  const fail = (reason: string): PlanResult => ({
    feasible: false,
    reason,
    totalCost: 0,
    totalCuts: 0,
    totalBars: 0,
    bars: [],
    minUsefulRemnant: 0,
  });

  if (!(kerf >= 0)) return fail('kerf 必须为非负数');
  if (input.stocks.length === 0) return fail('至少需要一种原料棒');
  for (const s of input.stocks) {
    if (!(s.length > 0) || !(s.price >= 0)) return fail('原料棒长度必须为正、价格非负');
  }

  // 展开订单为单件列表
  const pieces: number[] = [];
  for (const d of input.demands) {
    if (!(d.length > 0) || !Number.isInteger(d.count) || d.count < 0) {
      return fail('订单长度必须为正、件数为非负整数');
    }
    for (let i = 0; i < d.count; i++) pieces.push(d.length);
  }
  const n = pieces.length;
  if (n === 0) return fail('订单为空');
  if (n > MAX_PIECES) return fail(`需求总件数 ${n} 超过上限 ${MAX_PIECES}`);

  const stocks = input.stocks;
  const minUsefulRemnant = Math.min(...pieces);

  const N = 1 << n;
  // 每个子集的件数与总长
  const count = new Uint8Array(N);
  const sumLen = new Float64Array(N);
  for (let mask = 1; mask < N; mask++) {
    const low = mask & -mask;
    const idx = 31 - Math.clz32(low);
    count[mask] = count[mask ^ low] + 1;
    sumLen[mask] = sumLen[mask ^ low] + pieces[idx];
  }

  // bestBar[mask]：把 mask 这批件放在一根新棒上切的最优选择
  // （先比价格，再比刀数；对全局字典序目标而言逐组合取最优是安全的）
  const bestStock = new Int32Array(N).fill(-1);
  const bestCuts = new Uint8Array(N);
  const bestRemnant = new Float64Array(N);
  const bestPrice = new Float64Array(N);
  for (let mask = 1; mask < N; mask++) {
    const k = count[mask];
    const s = sumLen[mask];
    for (let t = 0; t < stocks.length; t++) {
      const fit = fitOnBar(s, k, stocks[t].length, kerf);
      if (!fit) continue;
      if (
        bestStock[mask] < 0 ||
        stocks[t].price < bestPrice[mask] - EPS ||
        (Math.abs(stocks[t].price - bestPrice[mask]) <= EPS && fit.cuts < bestCuts[mask])
      ) {
        bestStock[mask] = t;
        bestCuts[mask] = fit.cuts;
        bestRemnant[mask] = fit.remnant;
        bestPrice[mask] = stocks[t].price;
      }
    }
  }

  // 子集 DP：dp[mask] = 完成 mask 这批订单的最优 (成本, 刀数, 根数)
  const dpCost = new Float64Array(N).fill(Infinity);
  const dpCuts = new Uint32Array(N);
  const dpBars = new Uint8Array(N);
  const choice = new Int32Array(N).fill(-1); // 记录 mask 最后切下的子集
  dpCost[0] = 0;

  for (let mask = 1; mask < N; mask++) {
    const low = mask & -mask;
    // 只枚举包含最低位的子集，避免重复划分
    for (let sub = mask; sub > 0; sub = (sub - 1) & mask) {
      if ((sub & low) === 0) continue;
      const t = bestStock[sub];
      if (t < 0) continue;
      const prev = mask ^ sub;
      if (dpCost[prev] === Infinity) continue;
      const c = dpCost[prev] + stocks[t].price;
      const cu = dpCuts[prev] + bestCuts[sub];
      const b = dpBars[prev] + 1;
      const better =
        c < dpCost[mask] - EPS ||
        (Math.abs(c - dpCost[mask]) <= EPS &&
          (cu < dpCuts[mask] || (cu === dpCuts[mask] && b < dpBars[mask])));
      if (better) {
        dpCost[mask] = c;
        dpCuts[mask] = cu;
        dpBars[mask] = b;
        choice[mask] = sub;
      }
    }
  }

  const full = N - 1;
  if (choice[full] < 0) {
    const tooLong = Math.max(...pieces);
    const maxStock = Math.max(...stocks.map((s) => s.length));
    return fail(
      `存在无法单独放入任何原料棒的订单件（最长订单 ${tooLong}，最长原料 ${maxStock}）`,
    );
  }

  // 回溯构造每根棒的切法
  const bars: BarPlan[] = [];
  let mask = full;
  while (mask > 0) {
    const sub = choice[mask];
    const t = bestStock[sub];
    const barPieces: number[] = [];
    for (let i = 0; i < n; i++) {
      if (sub & (1 << i)) barPieces.push(pieces[i]);
    }
    barPieces.sort((a, b) => b - a);
    const remnant = bestRemnant[sub];
    bars.push({
      stock: stocks[t],
      pieces: barPieces,
      cuts: bestCuts[sub],
      remnant,
      remnantFate: remnant >= minUsefulRemnant - EPS ? 'reusable' : 'scrap',
    });
    mask ^= sub;
  }

  return {
    feasible: true,
    totalCost: dpCost[full],
    totalCuts: dpCuts[full],
    totalBars: dpBars[full],
    bars,
    minUsefulRemnant,
  };
}

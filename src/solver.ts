/**
 * 拆单求解器：为订单选择发货仓并分配商品数量。
 *
 * 模型：
 * - 每个仓有启用运费 baseFee（从该仓发货即收一次）、单件运费 perItemFee、按商品库存 stock。
 * - 可选免运门槛 freeShippingThreshold：从该仓发货总件数 >= 门槛时，该仓运费全免。
 *   因此"多取一件"（超出订单需求购买）可能反而更便宜，求解器允许超量分配（不超过库存）。
 * - 同种商品可拆分到多个仓。
 *
 * 目标（字典序）：
 *   1. 总运费最低；
 *   2. 包裹数（实际使用的仓库数）最少；
 *   3. 超需求件数最少（仅作并列时的收尾裁决）。
 *
 * 算法：
 * - 仓库数 <= 8，枚举每个仓的状态（不用 / 付费模式 / 免运模式），最多 3^8 = 6561 种组合。
 * - 每种组合下，问题化为带下界的最小费用循环流（运输问题，约束矩阵全幺模，整数解有保证）：
 *     S -> W_w  [下界: 付费=1, 免运=threshold; 上界: 付费=threshold-1, 免运=库存总量]
 *     W_w -> P_p [上界: 库存; 单位费用: 付费=perItemFee, 免运=0]
 *     P_p -> T  [下界: 需求量（允许超量，上界无穷）]
 *     T -> S    [回流边，构成循环流]
 * - 对所有组合取字典序最优，枚举是完备的，因此结果为精确最优。
 * - 同时输出一个最优性下界（松弛固定费用与门槛耦合的运输松弛），用于衡量最优间隙。
 */

export interface WarehouseInput {
  id: string;
  /** 启用运费：只要从该仓发货就收取一次 */
  baseFee: number;
  /** 单件运费 */
  perItemFee: number;
  /** 免运门槛：从该仓发出的总件数达到该值时，该仓运费全免 */
  freeShippingThreshold?: number;
  /** 商品 -> 库存件数 */
  stock: Record<string, number>;
}

export interface OrderLine {
  productId: string;
  quantity: number;
}

export interface Allocation {
  warehouseId: string;
  productId: string;
  quantity: number;
}

export interface SolveSuccess {
  status: "optimal";
  /** 最低总运费 */
  totalCost: number;
  /** 包裹数（实际发货的仓库数） */
  packages: number;
  /** 超出订单需求的件数（为凑免运门槛多买的部分） */
  extraUnits: number;
  allocations: Allocation[];
  /** 最优性下界：任何可行方案的费用都不低于该值 */
  lowerBound: number;
}

export interface SolveInfeasible {
  status: "infeasible";
  reason: string;
  lowerBound: number;
}

export type SolveResult = SolveSuccess | SolveInfeasible;

export const MAX_WAREHOUSES = 8;
export const MAX_PRODUCTS = 12;

const INF = Math.floor(Number.MAX_SAFE_INTEGER / 4);

interface FlowEdge {
  to: number;
  rev: number;
  cap: number;
  cost: number;
}

/** 带下界的最小费用循环流（SPFA 逐路增广，规模极小，足够快） */
class MinCostCirculation {
  private graph: FlowEdge[][] = [];
  private balance: number[] = [];
  private lowers: number[] = [];
  private edgeFrom: number[] = [];
  private edgeIndex: number[] = [];

  addNode(): number {
    this.graph.push([]);
    this.balance.push(0);
    return this.graph.length - 1;
  }

  /** 返回边句柄，求解后可用 flowOn 取流量 */
  addEdge(u: number, v: number, lower: number, upper: number, cost: number): number {
    const id = this.lowers.length;
    this.lowers.push(lower);
    this.balance[u] -= lower;
    this.balance[v] += lower;
    this.graph[u].push({ to: v, rev: this.graph[v].length, cap: upper - lower, cost });
    this.graph[v].push({ to: u, rev: this.graph[u].length - 1, cap: 0, cost: -cost });
    this.edgeFrom.push(u);
    this.edgeIndex.push(this.graph[u].length - 1);
    return id;
  }

  flowOn(id: number): number {
    const u = this.edgeFrom[id];
    const e = this.graph[u][this.edgeIndex[id]];
    return this.lowers[id] + this.graph[e.to][e.rev].cap;
  }

  /** 求解可行且费用最小的循环流；不可行时返回 null */
  solve(): { cost: number } | null {
    const ss = this.addNode();
    const tt = this.addNode();
    let required = 0;
    for (let v = 0; v < ss; v++) {
      if (this.balance[v] > 0) {
        this.addRawEdge(ss, v, this.balance[v], 0);
        required += this.balance[v];
      } else if (this.balance[v] < 0) {
        this.addRawEdge(v, tt, -this.balance[v], 0);
      }
    }
    const { flow, cost } = this.minCostMaxFlow(ss, tt, required);
    if (flow < required) return null;
    return { cost };
  }

  private addRawEdge(u: number, v: number, cap: number, cost: number): void {
    this.graph[u].push({ to: v, rev: this.graph[v].length, cap, cost });
    this.graph[v].push({ to: u, rev: this.graph[u].length - 1, cap: 0, cost: -cost });
  }

  private minCostMaxFlow(s: number, t: number, maxFlow: number): { flow: number; cost: number } {
    const n = this.graph.length;
    let flow = 0;
    let cost = 0;
    const prevV = new Array<number>(n);
    const prevE = new Array<number>(n);
    while (flow < maxFlow) {
      const dist = new Array<number>(n).fill(Infinity);
      const inQueue = new Array<boolean>(n).fill(false);
      dist[s] = 0;
      const queue: number[] = [s];
      inQueue[s] = true;
      for (let head = 0; head < queue.length; head++) {
        const u = queue[head];
        inQueue[u] = false;
        for (let i = 0; i < this.graph[u].length; i++) {
          const e = this.graph[u][i];
          if (e.cap > 0 && dist[e.to] > dist[u] + e.cost) {
            dist[e.to] = dist[u] + e.cost;
            prevV[e.to] = u;
            prevE[e.to] = i;
            if (!inQueue[e.to]) {
              queue.push(e.to);
              inQueue[e.to] = true;
            }
          }
        }
      }
      if (dist[t] === Infinity) break;
      let d = maxFlow - flow;
      for (let v = t; v !== s; v = prevV[v]) {
        d = Math.min(d, this.graph[prevV[v]][prevE[v]].cap);
      }
      for (let v = t; v !== s; v = prevV[v]) {
        const e = this.graph[prevV[v]][prevE[v]];
        e.cap -= d;
        this.graph[v][e.rev].cap += d;
      }
      flow += d;
      cost += d * dist[t];
    }
    return { flow, cost };
  }
}

function totalDemandOf(demands: Map<string, number>): number {
  let sum = 0;
  for (const q of demands.values()) sum += q;
  return sum;
}

/**
 * 最优性下界（松弛）：
 * - 单位运费松弛：有免运门槛的仓其边际件运费可能为 0（免运模式），取下界 0；否则取 perItemFee。
 *   按商品独立地用最低边际费率满足需求（忽略固定费用与门槛耦合）。
 * - 启用费松弛：若没有任何仓可能免运，则至少产生一次最小启用费。
 * 结果保证 <= 精确最优值。
 */
export function costLowerBound(
  warehouses: WarehouseInput[],
  demands: Map<string, number>,
): number {
  let lb = 0;
  let anyDemand = false;
  let freePossible = false;
  let minBaseFee = Infinity;
  for (const [productId, need] of demands) {
    if (need <= 0) continue;
    anyDemand = true;
    const rates: { rate: number; qty: number }[] = [];
    for (const w of warehouses) {
      const qty = w.stock[productId] ?? 0;
      if (qty <= 0) continue;
      if (w.freeShippingThreshold != null) {
        freePossible = true;
        rates.push({ rate: 0, qty });
      } else {
        minBaseFee = Math.min(minBaseFee, w.baseFee);
        rates.push({ rate: w.perItemFee, qty });
      }
    }
    rates.sort((a, b) => a.rate - b.rate);
    let remaining = need;
    for (const r of rates) {
      const take = Math.min(remaining, r.qty);
      lb += take * r.rate;
      remaining -= take;
      if (remaining === 0) break;
    }
  }
  if (anyDemand && !freePossible && minBaseFee < Infinity) lb += minBaseFee;
  return lb;
}

export function solveOrderSplitting(
  warehouses: WarehouseInput[],
  order: OrderLine[],
): SolveResult {
  if (warehouses.length === 0) throw new Error("至少需要一个仓库");
  if (warehouses.length > MAX_WAREHOUSES) {
    throw new Error(`仓库数超过上限 ${MAX_WAREHOUSES}`);
  }
  const demands = new Map<string, number>();
  for (const line of order) {
    if (!Number.isInteger(line.quantity) || line.quantity < 0) {
      throw new Error(`非法需求数量: ${line.productId} x ${line.quantity}`);
    }
    if (line.quantity > 0) {
      demands.set(line.productId, (demands.get(line.productId) ?? 0) + line.quantity);
    }
  }
  if (demands.size > MAX_PRODUCTS) {
    throw new Error(`商品种类超过上限 ${MAX_PRODUCTS}`);
  }
  for (const w of warehouses) {
    if (w.baseFee < 0 || w.perItemFee < 0) throw new Error(`运费不能为负: ${w.id}`);
    if (w.freeShippingThreshold != null && w.freeShippingThreshold < 1) {
      throw new Error(`免运门槛必须 >= 1: ${w.id}`);
    }
  }

  const lowerBound = costLowerBound(warehouses, demands);
  const totalDemand = totalDemandOf(demands);
  if (totalDemand === 0) {
    return { status: "optimal", totalCost: 0, packages: 0, extraUnits: 0, allocations: [], lowerBound: 0 };
  }

  const products = [...demands.keys()];
  const totalStock = new Array<number>(warehouses.length).fill(0);
  for (let i = 0; i < warehouses.length; i++) {
    for (const q of Object.values(warehouses[i].stock)) {
      if (!Number.isInteger(q) || q < 0) throw new Error(`非法库存: ${warehouses[i].id}`);
      totalStock[i] += q;
    }
  }
  for (const [productId, need] of demands) {
    let available = 0;
    for (const w of warehouses) available += w.stock[productId] ?? 0;
    if (available < need) {
      return {
        status: "infeasible",
        reason: `商品 ${productId} 总库存 ${available} 不足需求 ${need}`,
        lowerBound,
      };
    }
  }

  // 每个仓的可选状态：0=不用，1=付费模式，2=免运模式（仅有门槛时可选）
  const choices: number[][] = warehouses.map((w) =>
    w.freeShippingThreshold != null ? [0, 1, 2] : [0, 1],
  );

  let best: { cost: number; packages: number; extra: number; allocations: Allocation[] } | null =
    null;

  const regime = new Array<number>(warehouses.length).fill(0);
  const enumerate = (idx: number): void => {
    if (idx === warehouses.length) {
      const candidate = evaluateRegime(regime);
      if (
        candidate &&
        (!best ||
          candidate.cost < best.cost ||
          (candidate.cost === best.cost &&
            (candidate.packages < best.packages ||
              (candidate.packages === best.packages && candidate.extra < best.extra))))
      ) {
        best = candidate;
      }
      return;
    }
    for (const r of choices[idx]) {
      regime[idx] = r;
      enumerate(idx + 1);
    }
    regime[idx] = 0;
  };

  const evaluateRegime = (
    reg: number[],
  ): { cost: number; packages: number; extra: number; allocations: Allocation[] } | null => {
    const mcf = new MinCostCirculation();
    const S = mcf.addNode();
    const T = mcf.addNode();
    const wNode = warehouses.map(() => mcf.addNode());
    const pNode = products.map(() => mcf.addNode());

    let constant = 0;
    let packages = 0;
    for (let i = 0; i < warehouses.length; i++) {
      const w = warehouses[i];
      if (reg[i] === 0) continue;
      packages++;
      if (reg[i] === 1) {
        // 付费模式：至少发 1 件；若有门槛，不能超过 threshold-1（否则实际应免运）
        constant += w.baseFee;
        const upper = w.freeShippingThreshold != null
          ? Math.min(totalStock[i], w.freeShippingThreshold - 1)
          : totalStock[i];
        if (upper < 1) return null;
        mcf.addEdge(S, wNode[i], 1, upper, 0);
      } else {
        // 免运模式：必须达到门槛
        const threshold = w.freeShippingThreshold!;
        if (totalStock[i] < threshold) return null;
        mcf.addEdge(S, wNode[i], threshold, totalStock[i], 0);
      }
    }
    if (packages === 0) return null;

    const itemEdges: { w: number; p: number; edge: number }[] = [];
    for (let i = 0; i < warehouses.length; i++) {
      if (reg[i] === 0) continue;
      const unitCost = reg[i] === 1 ? warehouses[i].perItemFee : 0;
      for (let j = 0; j < products.length; j++) {
        const stock = warehouses[i].stock[products[j]] ?? 0;
        if (stock <= 0) continue;
        const edge = mcf.addEdge(wNode[i], pNode[j], 0, stock, unitCost);
        itemEdges.push({ w: i, p: j, edge });
      }
    }
    for (let j = 0; j < products.length; j++) {
      mcf.addEdge(pNode[j], T, demands.get(products[j])!, INF, 0);
    }
    mcf.addEdge(T, S, 0, INF, 0);

    const solved = mcf.solve();
    if (!solved) return null;

    const allocations: Allocation[] = [];
    let units = 0;
    for (const { w, p, edge } of itemEdges) {
      const q = mcf.flowOn(edge);
      if (q > 0) {
        allocations.push({ warehouseId: warehouses[w].id, productId: products[p], quantity: q });
        units += q;
      }
    }
    return { cost: constant + solved.cost, packages, extra: units - totalDemand, allocations };
  };

  enumerate(0);

  if (!best) {
    return { status: "infeasible", reason: "库存或门槛约束下无可行分配", lowerBound };
  }
  const result = best as { cost: number; packages: number; extra: number; allocations: Allocation[] };
  return {
    status: "optimal",
    totalCost: result.cost,
    packages: result.packages,
    extraUnits: result.extra,
    allocations: result.allocations,
    lowerBound,
  };
}

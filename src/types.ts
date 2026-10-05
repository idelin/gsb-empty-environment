/** 场景与结果类型定义。下标 0 始终表示仓库，站点下标从 1 开始。 */

export interface StationSpec {
  name: string;
  /** 站点容量（最大可停放车辆数） */
  capacity: number;
  /** 当前库存 */
  initial: number;
  /** 次日需求（期末目标库存） */
  target: number;
}

export interface VehicleSpec {
  /** 车载上限 */
  capacity: number;
  /** 出仓时已装载的车辆数，默认 0 */
  initialLoad?: number;
}

export interface DepotSpec {
  /** 仓库可提供的车辆数，默认无限 */
  supply?: number;
  /** 是否允许把车上车辆卸到仓库，默认 true */
  canUnload?: boolean;
}

export interface CostSpec {
  /** 每公里里程成本 */
  perKm: number;
  /** 期末每缺一辆车的罚金 */
  shortagePerBike: number;
  /** 期末每超目标一辆车的罚金，默认 0 */
  overagePerBike?: number;
}

export interface Scenario {
  name?: string;
  stations: StationSpec[];
  vehicle: VehicleSpec;
  depot?: DepotSpec;
  costs: CostSpec;
  /** (n+1)x(n+1) 距离矩阵（公里），第 0 行/列为仓库 */
  distanceMatrix: number[][];
  /** 转移量枚举模式：exact 精确，candidates 断点候选（快），auto 自动（默认） */
  transferMode?: "exact" | "candidates" | "auto";
  /** 状态搜索上限，默认 2_000_000 */
  maxIterations?: number;
}

/** 一次移动：从 from 到 to，并在 to 处转移 transfer 辆车。
 *  transfer > 0：从车上放下到站点/仓库；transfer < 0：从站点/仓库取上车。 */
export interface Move {
  from: number;
  to: number;
  transfer: number;
  distance: number;
}

export interface Plan {
  moves: Move[];
  distanceKm: number;
  mileageCost: number;
  shortageBikes: number;
  overageBikes: number;
  penaltyCost: number;
  totalCost: number;
  finalInventories: number[];
}

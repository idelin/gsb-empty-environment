/** 仓库（车辆出发点与终点） */
export interface Depot {
  x: number;
  y: number;
}

/** 站点定义 */
export interface Station {
  id: string;
  x: number;
  y: number;
  /** 站点容量（最大可停放车辆数） */
  capacity: number;
  /** 当前（调度前）库存 */
  initial: number;
  /** 次日需求（期望调度后达到的车辆数） */
  demand: number;
}

/** 调度问题输入 */
export interface Problem {
  depot: Depot;
  /** 车辆载重上限 */
  vehicleCapacity: number;
  /** 每公里里程成本 */
  costPerKm: number;
  /** 每缺一辆车的罚金 */
  penaltyPerBike: number;
  stations: Station[];
  /** 最多停靠次数（含回仓），默认 2 * 站点数 + 4 */
  maxStops?: number;
  /** 束搜索宽度，默认 4000 */
  beamWidth?: number;
}

/** 路线中的一步：从 from 行驶到 to，并在 to 执行装卸 */
export interface MoveStep {
  /** 起点，-1 表示仓库，否则为站点下标 */
  from: number;
  /** 终点，-1 表示仓库，否则为站点下标 */
  to: number;
  /** 本步行驶里程 */
  driveKm: number;
  /** 在终点站点的装卸量：>0 卸车（站点增加），<0 装车（站点减少），仓库处恒为 0 */
  delta: number;
  /** 本步结束后车载库存 */
  loadAfter: number;
  /** 本步结束后各站点库存 */
  invAfter: number[];
}

/** 求解结果 */
export interface Solution {
  steps: MoveStep[];
  totalKm: number;
  mileageCost: number;
  /** 各站点最终缺车数 max(0, min(需求, 容量) - 最终库存) */
  shortage: number[];
  /** 缺车罚金合计 */
  penalty: number;
  /** 总成本 = 里程成本 + 缺车罚金 */
  totalCost: number;
}

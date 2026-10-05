export interface Batch {
  id: string;
  origin: string;
  destination: string;
  /** 货物在起点可交付运输的时刻，绝对分钟（第 1 天 00:00 为 0，跨午夜 > 1440） */
  readyTime: number;
  /** 占用的运力单位数，批次不可拆分 */
  size: number;
}

export interface Trip {
  id: string;
  from: string;
  to: string;
  /** 发车时刻，绝对分钟 */
  depart: number;
  /** 到达时刻，绝对分钟，跨午夜则大于 1440 */
  arrive: number;
  capacity: number;
  /** 只要该班次载货（≥1 批）即收取一次 */
  fixedCost: number;
  /** 每运力单位费用，缺省为 0 */
  perUnitCost?: number;
}

export interface Handling {
  /** 装货时间：货物须在 depart - loadTime 之前到达装车站点 */
  loadTime: number;
  /** 卸货时间：货物在 arrive + unloadTime 之后才可被提取/转运 */
  unloadTime: number;
}

export interface Problem {
  batches: Batch[];
  trips: Trip[];
  handling: Handling;
}

export interface TripLeg {
  tripId: string;
  from: string;
  to: string;
  depart: number;
  arrive: number;
}

export interface PlannedMove {
  trip: Trip;
  batchIds: string[];
  /** 最迟开始装货时刻 = depart - loadTime */
  loadBy: number;
  /** 卸货完成、货物可提时刻 = arrive + unloadTime */
  availableAt: number;
  totalSize: number;
  cost: number;
}

export interface BatchResult {
  batchId: string;
  legs: TripLeg[];
  /** 在目的地可提取的绝对分钟 */
  arrivedAt: number;
}

export interface Plan {
  feasible: boolean;
  totalCost: number;
  moves: PlannedMove[];
  batches: BatchResult[];
  undeliverable: string[];
  reason?: string;
}

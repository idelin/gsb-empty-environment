/** 箱型定义：整箱采购、整箱配送到单一门店，不可拆箱。 */
export interface BoxType {
  id: string;
  /** 每箱容量（件） */
  capacity: number;
  /** 每箱采购成本 */
  purchaseCost: number;
  /** 每箱默认运输成本 */
  transportCost: number;
  /** 可选：按门店覆盖运输成本（如距离不同） */
  transportCostByStore?: Record<string, number>;
}

/** 门店定义：允许少量欠供，按缺货件数支付罚金。 */
export interface Store {
  id: string;
  /** 需求（件） */
  demand: number;
  /** 每欠供 1 件的罚金 */
  shortagePenaltyPerUnit: number;
  /** 可选：允许的最大欠供件数（“少量欠供”的上限），缺省为不限 */
  maxShortage?: number;
}

export interface PlanInput {
  boxTypes: BoxType[];
  stores: Store[];
}

/** 单门店的箱型分配结果 */
export interface StorePlan {
  storeId: string;
  /** 各箱型分配的箱数（与输入 boxTypes 顺序一致） */
  boxes: Record<string, number>;
  totalBoxes: number;
  /** 实际送达件数 */
  delivered: number;
  demand: number;
  /** 欠供件数（需付罚金） */
  shortage: number;
  /** 剩余库存（整箱送达超出需求、留在本门店的件数，不跨店流转） */
  leftover: number;
  purchaseCost: number;
  transportCost: number;
  shortagePenalty: number;
  totalCost: number;
}

export interface Plan {
  stores: StorePlan[];
  /** 全局剩余库存（各门店剩余之和，禁止跨店无偿调拨） */
  totalLeftover: number;
  totalDelivered: number;
  totalShortage: number;
  purchaseCost: number;
  transportCost: number;
  shortagePenalty: number;
  totalCost: number;
}

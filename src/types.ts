/** 箱型：整箱采购、整箱配送，不可拆分 */
export interface BoxType {
  id: string;
  /** 每箱可容纳的商品件数 */
  capacity: number;
  /** 每箱采购成本 */
  unitCost: number;
}

/** 门店 */
export interface Store {
  id: string;
  /** 需求件数 */
  demand: number;
  /** 每欠供 1 件的罚金 */
  shortagePenalty: number;
  /** 可选：配送到该门店的每箱运输成本（覆盖全局值） */
  transportCostPerBox?: number;
}

/** 问题输入 */
export interface Problem {
  boxTypes: BoxType[];
  stores: Store[];
  /** 全局每箱运输成本，默认 0 */
  transportCostPerBox?: number;
}

/** 某门店分配到的一种箱型数量 */
export interface BoxAllocation {
  boxTypeId: string;
  count: number;
  capacity: number;
}

/** 单门店配送方案 */
export interface StorePlan {
  storeId: string;
  boxes: BoxAllocation[];
  /** 实际配送到店的总件数 */
  unitsSupplied: number;
  /** 欠供件数 */
  shortage: number;
  /** 超出需求的件数（该店剩余库存） */
  leftover: number;
  procurementCost: number;
  transportCost: number;
  shortageCost: number;
  totalCost: number;
}

/** 整体求解结果 */
export interface Solution {
  storePlans: StorePlan[];
  /** 各箱型采购总箱数 */
  totalBoxes: BoxAllocation[];
  totalProcurementCost: number;
  totalTransportCost: number;
  totalShortageCost: number;
  totalCost: number;
  /** 全部门店剩余库存合计（超供件数） */
  totalLeftover: number;
  totalShortage: number;
}

/** 备件规格：初始库存与采购单价（缺省 price 表示该备件不可外购）。 */
export interface PartSpec {
  stock: number;
  price?: number;
}

/** 单台设备。 */
export interface Device {
  id: string;
  /** 修复人工/材料费（占用预算，非负整数）。 */
  repairCost: number;
  /** 修复后的整机价值。 */
  repairedValue: number;
  /** 残值：保持现状（闲置）时仍拥有的价值；拆解后残值归零。 */
  residualValue: number;
  /** 拆解费用（可选，默认 0，占用预算）。 */
  dismantleCost?: number;
  /** 本机拆解回收率（可选，覆盖全局 recoveryRate）。 */
  recoveryRate?: number;
  /** 修复所需备件齐套清单：备件名 -> 数量。 */
  needs: Record<string, number>;
  /** 本机内含可拆解回收的备件：备件名 -> 数量（回收损耗前）。 */
  contains: Record<string, number>;
}

/** 问题输入。金额一律为非负整数；设备数不得超过 18。 */
export interface Problem {
  budget: number;
  /** 全局拆解回收率，0~1，默认 1。实际回收量 = floor(contains * rate)。 */
  recoveryRate?: number;
  parts: Record<string, PartSpec>;
  devices: Device[];
}

export type Decision = "repair" | "dismantle" | "idle";

/** 一条备件流水：从来源（库存 / 采购 / 被拆解设备）流向用途（被修复设备）。 */
export interface Flow {
  from: string;
  to: string;
  part: string;
  qty: number;
}

export interface Solution {
  /** 方案总价值 = Σ修复设备修复价值 + Σ闲置设备残值（被拆解设备残值损失）。 */
  totalValue: number;
  budgetUsed: number;
  decisions: Record<string, Decision>;
  /** 各备件的外购数量。 */
  purchases: Record<string, number>;
  /** 来源到用途的备件流水（含库存、采购、拆解回收三条来源）。 */
  flows: Flow[];
  /** 拆解后未被使用的回收备件（有损耗，剩余即报废）。 */
  leftoverRecovered: Flow[];
  stats: { nodes: number };
}

/** 备件数量表：备件 ID -> 数量 */
export type PartQty = Record<string, number>;

/** 单台设备 */
export interface Device {
  /** 设备唯一 ID */
  id: string;
  /** 显示名称（可选，缺省用 id） */
  name?: string;
  /** 修复所需预算 */
  repairCost: number;
  /** 修复后恢复的价值 */
  repairValue: number;
  /** 拆解所损失的残值（作为机会成本从目标中扣除） */
  residualValue: number;
  /** 修复所需备件清单（必须齐套才能修） */
  repairParts: PartQty;
  /** 拆解可回收的备件（回收率作用于每一项） */
  salvageParts: PartQty;
}

/** 规划问题输入 */
export interface Problem {
  /** 总预算（只计修复费用） */
  budget: number;
  /** 期初备件库存 */
  stock: PartQty;
  /** 全局回收率 (0,1]，损耗 = 数量 * (1 - 回收率) */
  recoveryYield: number;
  /** 按备件覆盖回收率（可选，优先于全局回收率） */
  partYield?: PartQty;
  /** 设备列表，最多 18 台 */
  devices: Device[];
}

/** 单台设备的处置决策：修复 / 拆解 / 闲置（三者互斥） */
export type Decision = 'repair' | 'dismantle' | 'idle';

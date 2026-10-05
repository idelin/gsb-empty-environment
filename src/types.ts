/** 物料需求 */
export interface MaterialSpec {
  id: string;
  /** 需求数量（单位：个/件） */
  demand: number;
  /** 允许超购的绝对数量上限；缺省用全局 maxOverbuyRatio 折算 */
  maxOverbuy?: number;
}

/** 供应商对某物料的报价与供货约束 */
export interface SupplierItem {
  materialId: string;
  unitPrice: number;
  /** 起订量：一旦购买该物料，数量必须 >= moq（默认 0） */
  moq?: number;
  /** 整包倍数：购买数量必须是 packMultiple 的整数倍（默认 1） */
  packMultiple?: number;
  /** 该物料的交付上限（默认不限） */
  maxQty?: number;
}

/** 整单阶梯折扣：折前小计 >= minSubtotal 时整单享 discountPct% 折扣 */
export interface DiscountTier {
  minSubtotal: number;
  /** 折扣百分比，如 8 表示整单打 92 折 */
  discountPct: number;
}

export interface SupplierSpec {
  id: string;
  name?: string;
  /** 固定运费：只要在该供应商下单即收一次 */
  freight?: number;
  /** 折前小计达到该值免运费 */
  freeFreightMinSubtotal?: number;
  /** 整单交付总量上限（所有物料件数之和） */
  maxTotalQty?: number;
  items: SupplierItem[];
  /** 按 minSubtotal 升序 */
  tiers?: DiscountTier[];
}

export interface Scenario {
  /** 全局默认超购比例上限，如 0.1 表示每种物料最多多买 10% */
  maxOverbuyRatio?: number;
  materials: MaterialSpec[];
  /** 最多 6 家 */
  suppliers: SupplierSpec[];
}

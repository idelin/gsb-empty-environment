/** 整单阶梯折扣：订单折前金额 >= minSubtotal 时，整单享受 discountRate 折扣。 */
export interface DiscountTier {
  minSubtotal: number;
  discountRate: number; // 0 <= rate < 1，例如 0.1 表示整单九折
}

/** 运费规则：固定运费 + 按件运费；折前金额满 freeAboveSubtotal 时免除固定运费。 */
export interface FreightRule {
  fixed: number;
  perUnit?: number;
  freeAboveSubtotal?: number;
}

export interface SupplierItem {
  materialId: string;
  unitPrice: number;
  moq?: number;      // 起订量（件），默认 0
  packSize?: number; // 整包倍数（件/包），默认 1
  maxQty?: number;   // 该供应商对此物料的交付上限（件），默认无限
}

export interface Supplier {
  id: string;
  name?: string;
  items: SupplierItem[];
  capacity?: number; // 供应商总交付上限（件，跨物料合计），默认无限
  freight?: FreightRule;
  discountTiers?: DiscountTier[]; // 整单阶梯，按 minSubtotal 升序（内部会自动排序）
}

export interface MaterialDemand {
  materialId: string;
  demand: number;
  maxOverbuyRatio?: number; // 该物料允许的超购比例上限，覆盖全局默认
}

export interface Problem {
  demands: MaterialDemand[];
  suppliers: Supplier[];
  maxOverbuyRatio?: number; // 全局默认超购比例上限，默认 0（不允许超购）
  maxSuppliers?: number;    // 方案最多使用的供应商家数，默认 6
}

/** 求解结果的一行采购明细：从某供应商采某物料 packs 包。 */
export interface PurchaseLine {
  supplierId: string;
  materialId: string;
  packs: number;
}

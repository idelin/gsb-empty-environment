/** 订单中的一个商品行。 */
export interface OrderItem {
  sku: string;
  quantity: number;
}

/**
 * 仓库。
 * - fixedFee: 启用运费（只要从该仓发货即收取一次的固定运费）
 * - perItemFee: 单件运费（每件商品收取）
 * - freeShippingThreshold: 免运门槛，可选。当从该仓发出的总件数 >= 门槛时，
 *   该仓全部运费（启用运费 + 单件运费）免除。
 * - stock: 各 SKU 库存。
 */
export interface Warehouse {
  id: string;
  fixedFee: number;
  perItemFee: number;
  freeShippingThreshold?: number;
  stock: Record<string, number>;
}

export interface SolveOptions {
  /**
   * 是否允许超发（发货量可以超过订单需求量）。
   * 允许超发时，求解器可以利用免运门槛：多发一件凑满门槛反而总费用更低。
   * 默认 true。
   */
  allowOverDelivery?: boolean;
}

export interface WarehouseAllocation {
  warehouseId: string;
  /** sku -> 发货件数 */
  items: Record<string, number>;
  /** 该仓总发货件数 */
  totalItems: number;
  /** 该仓产生的运费 */
  shippingCost: number;
  /** 是否触发了免运门槛 */
  freeShippingApplied: boolean;
}

export interface SolveResult {
  status: 'optimal' | 'infeasible';
  /** 最优总运费（主目标） */
  totalCost: number | null;
  /** 最优解使用的包裹数 = 发货仓数量（次目标） */
  packages: number | null;
  /**
   * 最优性下界：对固定费用/免运门槛做松弛后的运输问题最优值，
   * 满足 lowerBound <= totalCost，用于证明最优性（gap = totalCost - lowerBound）。
   */
  lowerBound: number | null;
  gap: number | null;
  allocations: WarehouseAllocation[];
  /** sku -> 超出订单需求的发货件数（仅允许超发且凑门槛时可能非零） */
  overDelivery: Record<string, number>;
}

/**
 * 领域模型：所有金额单位均为「分」（整数）。
 */

/** 购物车中的一行商品 */
export interface CartLine {
  skuId: string;
  name?: string;
  /** 单价（分） */
  unitPrice: number;
  quantity: number;
}

export type CouponType = 'ITEM' | 'DISCOUNT' | 'THRESHOLD';

interface CouponBase {
  id: string;
  name?: string;
  /**
   * 互斥组：同一组内的券两两不能同时使用。
   */
  mutexGroup?: string;
  /**
   * 先后关系：列出的券必须先于本券结算（即本券生效的前提是这些券也被选中）。
   * 显式依赖优先于默认类型顺序（单品券 -> 折扣券 -> 满减券）；依赖成环的组合非法。
   */
  dependsOn?: string[];
}

/** 单品券：指定 SKU 每件的立减金额（分），整行优惠不超过该行剩余金额 */
export interface ItemCoupon extends CouponBase {
  type: 'ITEM';
  skuId: string;
  /** 每件立减（分） */
  amountOffPerUnit: number;
}

/** 折扣券：按百分比折，逐行向下取整；可限定 SKU 范围，默认全场 */
export interface DiscountCoupon extends CouponBase {
  type: 'DISCOUNT';
  /** 85 表示 85 折，即支付 85%。取值 1..100 */
  ratePercent: number;
  /** 限定生效的 SKU；缺省表示全部行 */
  skuIds?: string[];
}

/** 满减券：结算到本券时，当前应付金额达到门槛方可使用 */
export interface ThresholdCoupon extends CouponBase {
  type: 'THRESHOLD';
  /** 门槛（分），按使用本券时的当前应付金额判断 */
  threshold: number;
  /** 立减（分） */
  amountOff: number;
}

export type Coupon = ItemCoupon | DiscountCoupon | ThresholdCoupon;

/** 单步结算记录 */
export interface SettlementStep {
  couponId: string;
  couponName?: string;
  type: CouponType;
  /** 本券结算前的应付金额（分） */
  amountBefore: number;
  /** 本券贡献的优惠（分） */
  discount: number;
  /** 本券结算后的应付金额（分） */
  amountAfter: number;
  /** 逐行明细（单品券/折扣券），满减券为空 */
  lines: LineStepDetail[];
}

export interface LineStepDetail {
  skuId: string;
  quantity: number;
  amountBefore: number;
  discount: number;
  amountAfter: number;
}

/** 结算结果 */
export interface Settlement {
  /** 原始商品总额（分） */
  originalTotal: number;
  /** 最优实付（分） */
  payable: number;
  /** 总优惠（分） */
  totalDiscount: number;
  /** 选中的券 id，按结算顺序 */
  couponIds: string[];
  /** 逐步金额 */
  steps: SettlementStep[];
}

/** 退货重算结果 */
export interface RefundResult {
  skuId: string;
  quantity: number;
  /** 应退金额（分）= 原实付 - 重算后实付；理论上可能为负（退券损失大于商品金额） */
  refundAmount: number;
  /** 退货前的结算 */
  before: Settlement;
  /** 退货后重新计算的结算（非按比例退券） */
  after: Settlement;
}

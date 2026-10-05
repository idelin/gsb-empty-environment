/**
 * 领域类型定义。
 * 约定：所有金额一律为「分」（整数），折扣按行向下取整。
 */

/** 金额单位：分 */
export type Fen = number;

/** 购物车中的一行商品 */
export interface CartItem {
  id: string;
  name: string;
  /** 单价（分） */
  unitPrice: Fen;
  quantity: number;
}

export type CouponType = 'item' | 'discount' | 'threshold';

interface CouponBase {
  id: string;
  name: string;
  type: CouponType;
  /**
   * 互斥组标识。同组券最多选中一张；不设置则不互斥。
   */
  mutexGroup?: string;
  /**
   * 先后关系（前置依赖）：列出的券必须先于本券被选中，
   * 即本券生效的前提是这些券也在同一组合中。
   */
  requires?: string[];
  /**
   * 适用范围（商品行 id 列表）。缺省表示全场通用。
   */
  itemIds?: string[];
}

/** 单品券：指定商品每件立减固定金额 */
export interface ItemCoupon extends CouponBase {
  type: 'item';
  /** 目标商品行 id */
  itemId: string;
  /** 每件立减金额（分），按数量乘算，不超过该行剩余金额 */
  amountOff: Fen;
}

/** 折扣券：按百分比折让，逐行向下取整 */
export interface DiscountCoupon extends CouponBase {
  type: 'discount';
  /** 折后支付百分比，1-100。例如 88 表示 8.8 折（支付 88%） */
  percent: number;
}

/** 满减券：适用范围内金额达到门槛后立减 */
export interface ThresholdCoupon extends CouponBase {
  type: 'threshold';
  /** 门槛金额（分），按结算到本券时的实时金额判断 */
  threshold: Fen;
  /** 立减金额（分） */
  off: Fen;
}

export type Coupon = ItemCoupon | DiscountCoupon | ThresholdCoupon;

/** 单张券的结算步骤（逐步金额） */
export interface SettlementStep {
  couponId: string;
  couponName: string;
  type: CouponType;
  /** 本券结算前应付（分） */
  amountBefore: Fen;
  /** 本券优惠金额（分） */
  discount: Fen;
  /** 本券结算后应付（分） */
  amountAfter: Fen;
  /** 逐行优惠明细（分），便于核对折扣逐行取整 */
  lineBreakdown: Array<{ itemId: string; off: Fen }>;
}

/** 一次结算结果 */
export interface Settlement {
  /** 优惠前总额（分） */
  totalBefore: Fen;
  /** 总优惠（分） */
  totalDiscount: Fen;
  /** 最终应付（分） */
  payable: Fen;
  /** 选中的券，按实际结算顺序排列 */
  coupons: Coupon[];
  /** 逐步金额 */
  steps: SettlementStep[];
}

/** 退货重算结果 */
export interface ReturnResult {
  /** 退货前结算 */
  original: Settlement;
  /** 退货后重新计算的结算（重新选券，而非按比例退券） */
  afterReturn: Settlement;
  /** 应退金额（分）= 原应付 - 重算后应付 */
  refund: Fen;
  /** 被退的商品与数量 */
  returned: { itemId: string; quantity: number };
}

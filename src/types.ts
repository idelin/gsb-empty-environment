/** 领域模型：集团授信、子公司借款、汇率、担保物。 */

/** 集团授信额度（含协议预警线比例）。 */
export interface CreditLine {
  readonly groupId: string;
  readonly limitAmount: number;
  readonly currency: string;
  /** 预警线占授信额度的比例，例如 0.7 表示敞口达到额度 70% 即触碰警戒线。 */
  readonly warningRatio: number;
}

/** 子公司借款敞口。 */
export interface Loan {
  readonly subsidiary: string;
  readonly amount: number;
  readonly currency: string;
}

/** 汇率：1 单位 from 货币 = rate 单位 to 货币。 */
export interface FxRate {
  readonly from: string;
  readonly to: string;
  readonly rate: number;
}

/** 担保物（质押物）价值。 */
export interface Collateral {
  readonly owner: string;
  readonly value: number;
  readonly currency: string;
}

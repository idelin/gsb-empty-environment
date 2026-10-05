/** 权益额度的叠加规则：多次购买同类型权益时，总额度按规则计算。 */
export type BenefitRule = 'stack' | 'max';

/** 套餐登记输入。有效期为半开区间 [validFrom, validTo)，时间戳为 epoch 毫秒。 */
export interface PackageInput {
  packageId: string;
  validFrom: number;
  validTo: number;
  /** benefitType -> 次数额度（非负整数） */
  quotas: Record<string, number>;
}

/** 套餐追溯更正输入：只影响后续计算，绝不改写已确认消费。 */
export interface PackageCorrection {
  validFrom?: number;
  validTo?: number;
  quotas?: Record<string, number>;
}

/** 一笔消费在某个套餐上的扣减明细。 */
export interface Allocation {
  packageId: string;
  amount: number;
}

export type ConsumptionStatus = 'confirmed' | 'revoked';

/** 已确认的消费记录。allocations 一经写入即不可变。 */
export interface ConsumptionRecord {
  consumptionId: string;
  memberId: string;
  benefitType: string;
  amount: number;
  consumedAt: number;
  allocations: Allocation[];
  status: ConsumptionStatus;
  revokedAt: number | null;
}

/** 套餐某类权益的余额视图。 */
export interface PackageBalance {
  packageId: string;
  quota: number;
  consumed: number;
  remaining: number;
  validFrom: number;
  validTo: number;
  /** 在查询时刻是否处于有效区间内 */
  valid: boolean;
}

export interface BalanceView {
  memberId: string;
  benefitType: string;
  rule: BenefitRule;
  at: number;
  /** 按叠加规则计算出的可用总额度 */
  effective: number;
  packages: PackageBalance[];
}

export type LedgerEvent =
  | { seq: number; kind: 'benefit_type_registered'; benefitType: string; rule: BenefitRule }
  | { seq: number; kind: 'package_added'; memberId: string; packageId: string; input: PackageInput }
  | {
      seq: number;
      kind: 'package_corrected';
      memberId: string;
      packageId: string;
      before: PackageInput;
      after: PackageInput;
      correctedAt: number;
    }
  | { seq: number; kind: 'consumption_confirmed'; record: ConsumptionRecord }
  | { seq: number; kind: 'consumption_revoked'; memberId: string; consumptionId: string; revokedAt: number };

/** 追溯更正与已确认消费之间的冲突报告（只报告，不改写）。 */
export interface AllocationConflict {
  consumptionId: string;
  packageId: string;
  benefitType: string;
  reason: 'package_missing' | 'quota_missing' | 'not_valid_at_consumption' | 'quota_below_consumed';
  detail: string;
}

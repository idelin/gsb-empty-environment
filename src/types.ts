/** 权益合并规则：stack = 多套餐次数叠加；max = 取各套餐次数的最大值。 */
export type MergeRule = 'stack' | 'max';

/** 有效区间，左闭右开：[validFrom, validTo)，单位由调用方决定（建议 epoch ms）。 */
export interface TimeInterval {
  validFrom: number;
  validTo: number;
}

/** 套餐某一版本的授权内容。 */
export interface PackageVersion {
  interval: TimeInterval;
  /** benefitType -> 次数额度 */
  grants: Readonly<Record<string, number>>;
}

/** 一次消费中，从某个套餐来源扣减的数量。确认后不可变。 */
export interface Allocation {
  packageId: string;
  quantity: number;
}

export type ConsumptionStatus = 'confirmed' | 'revoked';

export interface ConsumptionRecord {
  id: string;
  benefitType: string;
  quantity: number;
  consumedAt: number;
  status: ConsumptionStatus;
  /** 确定性扣减产生的来源分配，确认后冻结，任何追溯更正都不会改写它。 */
  allocations: readonly Allocation[];
  revokedAt: number | null;
}

/** 撤销时返还给原来源套餐的明细。 */
export interface ReturnEntry {
  packageId: string;
  quantity: number;
  /** 返还额度所属的有效区间——即来源套餐当前的有效期，撤销本身不会延长它。 */
  usableAt: TimeInterval;
  /** 撤销发生时来源套餐是否已过期（过期则返还的次数立即可用性为 0）。 */
  expiredOnReturn: boolean;
}

export interface RevocationRecord {
  id: string;
  consumptionId: string;
  revokedAt: number;
  returns: readonly ReturnEntry[];
}

/** 套餐追溯更正记录：追加式版本历史，旧版本保留可查。 */
export interface PackageCorrection {
  seq: number;
  correctedAt: number;
  previous: PackageVersion;
  next: PackageVersion;
  reason: string | null;
}

export interface PackageBalance {
  packageId: string;
  interval: TimeInterval;
  /** 在查询时刻是否处于有效期内。 */
  active: boolean;
  quota: number;
  /** 已确认（未撤销）消费从该套餐扣减的总量。 */
  consumed: number;
  /** max(0, quota - consumed)。 */
  remaining: number;
  /**
   * 缺口：当追溯更正把额度降到已确认消费量之下时，consumed - quota 的部分。
   * 缺口以显式字段暴露，而不是悄悄改写历史分配。
   */
  deficit: number;
}

export interface Balance {
  benefitType: string;
  mergeRule: MergeRule;
  at: number;
  /** 当前有效套餐按合并规则得到的总额度（stack 求和 / max 取最大）。 */
  entitlement: number;
  /** 当前有效套餐上已确认未撤销的消耗总量。 */
  consumed: number;
  available: number;
  /** 当前有效套餐上的缺口总量（追溯更正导致）。 */
  deficit: number;
  packages: PackageBalance[];
}

/** 追加式审计事件。 */
export type LedgerEvent =
  | { kind: 'package_registered'; at: number; packageId: string; version: PackageVersion }
  | { kind: 'package_corrected'; packageId: string; correction: PackageCorrection }
  | { kind: 'consumption_confirmed'; consumption: ConsumptionRecord }
  | { kind: 'consumption_revoked'; revocation: RevocationRecord };

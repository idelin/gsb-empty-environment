import {
  AlreadyRevokedError,
  InsufficientQuotaError,
  UnknownBenefitTypeError,
  UnknownConsumptionError,
  UnknownPackageError,
  ValidationError,
} from './errors.ts';
import type {
  Allocation,
  Balance,
  ConsumptionRecord,
  LedgerEvent,
  MergeRule,
  PackageBalance,
  PackageCorrection,
  PackageVersion,
  RevocationRecord,
  TimeInterval,
} from './types.ts';

export interface AddPackageInput {
  id: string;
  validFrom: number;
  validTo: number;
  grants: Record<string, number>;
  /** 注册时间，仅用于审计事件，默认 0。 */
  at?: number;
}

export interface CorrectPackagePatch {
  validFrom?: number;
  validTo?: number;
  /** 提供时整体替换授权表（而非合并）。 */
  grants?: Record<string, number>;
}

export interface ConsumeInput {
  benefitType: string;
  quantity: number;
  /** 消费发生时刻，决定哪些套餐处于有效期内。 */
  at: number;
  /** 可选业务单号；缺省时由账本按 C-0001 递增生成，保证确定性。 */
  id?: string;
}

export interface ConsumptionFilter {
  benefitType?: string;
  status?: 'confirmed' | 'revoked';
}

interface PackageState {
  id: string;
  current: PackageVersion;
  corrections: PackageCorrection[];
}

function cloneVersion(version: PackageVersion): PackageVersion {
  return {
    interval: { ...version.interval },
    grants: Object.freeze({ ...version.grants }),
  };
}

function isActive(interval: TimeInterval, at: number): boolean {
  return interval.validFrom <= at && at < interval.validTo;
}

function assertPositiveInteger(value: number, what: string): void {
  if (!Number.isInteger(value) || value <= 0) {
    throw new ValidationError(`${what} 必须是正整数，收到 ${value}`);
  }
}

/**
 * 会员权益账本。
 *
 * 核心不变量：
 * 1. 扣减顺序确定：按 (validTo, validFrom, packageId) 升序，先到期先用。
 * 2. 已确认消费的 allocations 一旦写入即冻结，套餐追溯更正只追加新版本，
 *    绝不改写历史分配；额度被调低时以 deficit 显式暴露缺口。
 * 3. 撤销把次数精确返还原来源套餐，可用性完全由来源套餐自身的有效区间决定，
 *    撤销行为不会延长任何有效期。
 */
export class BenefitLedger {
  readonly #mergeRules = new Map<string, MergeRule>();
  readonly #packages = new Map<string, PackageState>();
  readonly #consumptions = new Map<string, ConsumptionRecord>();
  readonly #revocations = new Map<string, RevocationRecord>();
  readonly #events: LedgerEvent[] = [];
  #consumptionSeq = 0;
  #revocationSeq = 0;
  #correctionSeq = 0;

  registerBenefitType(code: string, mergeRule: MergeRule): void {
    if (this.#mergeRules.has(code)) {
      throw new ValidationError(`权益类型已注册：${code}`);
    }
    if (mergeRule !== 'stack' && mergeRule !== 'max') {
      throw new ValidationError(`非法合并规则：${mergeRule}`);
    }
    this.#mergeRules.set(code, mergeRule);
  }

  addPackage(input: AddPackageInput): void {
    if (this.#packages.has(input.id)) {
      throw new ValidationError(`套餐已存在：${input.id}`);
    }
    const version = this.#buildVersion(input.validFrom, input.validTo, input.grants);
    this.#packages.set(input.id, { id: input.id, current: version, corrections: [] });
    this.#events.push({
      kind: 'package_registered',
      at: input.at ?? 0,
      packageId: input.id,
      version: cloneVersion(version),
    });
  }

  /**
   * 追溯更正套餐。只追加一条更正记录并切换当前版本；
   * 已确认消费的 allocations 保持原样，缺口通过 getBalance 的 deficit 暴露。
   */
  correctPackage(
    packageId: string,
    patch: CorrectPackagePatch,
    opts: { at?: number; reason?: string } = {},
  ): PackageCorrection {
    const pkg = this.#requirePackage(packageId);
    const previous = cloneVersion(pkg.current);
    const next = this.#buildVersion(
      patch.validFrom ?? previous.interval.validFrom,
      patch.validTo ?? previous.interval.validTo,
      patch.grants ?? { ...previous.grants },
    );
    const correction: PackageCorrection = Object.freeze({
      seq: ++this.#correctionSeq,
      correctedAt: opts.at ?? 0,
      previous,
      next: cloneVersion(next),
      reason: opts.reason ?? null,
    });
    pkg.current = next;
    pkg.corrections.push(correction);
    this.#events.push({ kind: 'package_corrected', packageId, correction });
    return correction;
  }

  getPackageVersion(packageId: string): PackageVersion {
    return cloneVersion(this.#requirePackage(packageId).current);
  }

  getPackageCorrections(packageId: string): PackageCorrection[] {
    return [...this.#requirePackage(packageId).corrections];
  }

  /**
   * 确定性扣减：先校验总额度，再按固定顺序在有效套餐间分配。
   * 额度不足时整体失败，不产生任何部分扣减。
   */
  consume(input: ConsumeInput): ConsumptionRecord {
    this.#requireRule(input.benefitType);
    assertPositiveInteger(input.quantity, '消费次数');
    if (input.id !== undefined && this.#consumptions.has(input.id)) {
      throw new ValidationError(`消费单号重复：${input.id}`);
    }

    const balance = this.getBalance(input.benefitType, input.at);
    if (input.quantity > balance.available) {
      throw new InsufficientQuotaError(input.benefitType, input.quantity, balance.available);
    }

    const consumedByPackage = this.#confirmedConsumedByPackage(input.benefitType);
    const ordered = this.#orderedActivePackages(input.benefitType, input.at);
    const allocations: Allocation[] = [];
    let remaining = input.quantity;
    for (const pkg of ordered) {
      if (remaining === 0) break;
      const quota = pkg.current.grants[input.benefitType] ?? 0;
      const headroom = Math.max(0, quota - (consumedByPackage.get(pkg.id) ?? 0));
      const take = Math.min(headroom, remaining);
      if (take > 0) {
        allocations.push(Object.freeze({ packageId: pkg.id, quantity: take }));
        remaining -= take;
      }
    }
    if (remaining !== 0) {
      // 理论上不可达：available 的口径保证 Σheadroom >= available（stack 相等，
      // max 规则下 Σquota >= max(quota)）。若触发说明内部不变量被破坏。
      throw new Error(`内部错误：额度校验通过但分配不足，剩余 ${remaining}`);
    }

    const id = input.id ?? `C-${String(++this.#consumptionSeq).padStart(4, '0')}`;
    const record: ConsumptionRecord = {
      id,
      benefitType: input.benefitType,
      quantity: input.quantity,
      consumedAt: input.at,
      status: 'confirmed',
      allocations: Object.freeze(allocations),
      revokedAt: null,
    };
    this.#consumptions.set(id, record);
    this.#events.push({ kind: 'consumption_confirmed', consumption: this.#snapshot(record) });
    return this.#snapshot(record);
  }

  /**
   * 撤销消费：把次数精确返还原来源套餐。返还额度跟随来源套餐当前的有效期，
   * 已过期则返还立即可用性为 0——撤销不会延长任何有效期。
   */
  revoke(consumptionId: string, at: number): RevocationRecord {
    const record = this.#consumptions.get(consumptionId);
    if (!record) {
      throw new UnknownConsumptionError(`消费不存在：${consumptionId}`);
    }
    if (record.status === 'revoked') {
      throw new AlreadyRevokedError(`消费已撤销：${consumptionId}`);
    }
    record.status = 'revoked';
    record.revokedAt = at;

    const returns = record.allocations.map((alloc) => {
      const pkg = this.#requirePackage(alloc.packageId);
      const usableAt = { ...pkg.current.interval };
      return Object.freeze({
        packageId: alloc.packageId,
        quantity: alloc.quantity,
        usableAt,
        expiredOnReturn: at >= usableAt.validTo,
      });
    });

    const revocation: RevocationRecord = Object.freeze({
      id: `R-${String(++this.#revocationSeq).padStart(4, '0')}`,
      consumptionId,
      revokedAt: at,
      returns: Object.freeze(returns),
    });
    this.#revocations.set(revocation.id, revocation);
    this.#events.push({ kind: 'consumption_revoked', revocation });
    return revocation;
  }

  available(benefitType: string, at: number): number {
    return this.getBalance(benefitType, at).available;
  }

  getBalance(benefitType: string, at: number): Balance {
    const rule = this.#requireRule(benefitType);
    const consumedByPackage = this.#confirmedConsumedByPackage(benefitType);

    const packages: PackageBalance[] = [];
    for (const pkg of this.#packages.values()) {
      const quota = pkg.current.grants[benefitType];
      if (quota === undefined) continue;
      const consumed = consumedByPackage.get(pkg.id) ?? 0;
      const raw = quota - consumed;
      packages.push({
        packageId: pkg.id,
        interval: { ...pkg.current.interval },
        active: isActive(pkg.current.interval, at),
        quota,
        consumed,
        remaining: Math.max(0, raw),
        deficit: Math.max(0, -raw),
      });
    }
    packages.sort(comparePackageBalance);

    const active = packages.filter((p) => p.active);
    const entitlement =
      rule === 'stack'
        ? active.reduce((sum, p) => sum + p.quota, 0)
        : active.reduce((best, p) => Math.max(best, p.quota), 0);
    const consumed = active.reduce((sum, p) => sum + p.consumed, 0);
    const available =
      rule === 'stack'
        ? active.reduce((sum, p) => sum + p.remaining, 0)
        : Math.max(0, entitlement - consumed);
    const deficit = active.reduce((sum, p) => sum + p.deficit, 0);

    return { benefitType, mergeRule: rule, at, entitlement, consumed, available, deficit, packages };
  }

  getConsumption(id: string): ConsumptionRecord {
    const record = this.#consumptions.get(id);
    if (!record) {
      throw new UnknownConsumptionError(`消费不存在：${id}`);
    }
    return this.#snapshot(record);
  }

  /** 历史查询：返回快照副本，调用方无法篡改账本内部状态。 */
  getConsumptionHistory(filter: ConsumptionFilter = {}): ConsumptionRecord[] {
    const result: ConsumptionRecord[] = [];
    for (const record of this.#consumptions.values()) {
      if (filter.benefitType !== undefined && record.benefitType !== filter.benefitType) continue;
      if (filter.status !== undefined && record.status !== filter.status) continue;
      result.push(this.#snapshot(record));
    }
    return result;
  }

  getRevocationHistory(): RevocationRecord[] {
    return [...this.#revocations.values()];
  }

  /** 完整审计事件流：注册、更正、确认、撤销，按发生顺序。 */
  getEvents(): readonly LedgerEvent[] {
    return [...this.#events];
  }

  #buildVersion(validFrom: number, validTo: number, grants: Record<string, number>): PackageVersion {
    if (!(validFrom < validTo)) {
      throw new ValidationError(`有效区间非法：[${validFrom}, ${validTo})`);
    }
    const clean: Record<string, number> = {};
    for (const [type, quantity] of Object.entries(grants)) {
      if (!this.#mergeRules.has(type)) {
        throw new UnknownBenefitTypeError(`未注册的权益类型：${type}`);
      }
      if (!Number.isInteger(quantity) || quantity < 0) {
        throw new ValidationError(`权益 ${type} 的额度必须是非负整数，收到 ${quantity}`);
      }
      if (quantity > 0) clean[type] = quantity;
    }
    return {
      interval: Object.freeze({ validFrom, validTo }),
      grants: Object.freeze(clean),
    };
  }

  /** 某权益类型下，各套餐被已确认（未撤销）消费占用的次数。 */
  #confirmedConsumedByPackage(benefitType: string): Map<string, number> {
    const totals = new Map<string, number>();
    for (const record of this.#consumptions.values()) {
      if (record.benefitType !== benefitType || record.status !== 'confirmed') continue;
      for (const alloc of record.allocations) {
        totals.set(alloc.packageId, (totals.get(alloc.packageId) ?? 0) + alloc.quantity);
      }
    }
    return totals;
  }

  /** 确定性顺序：(validTo, validFrom, packageId) 升序。 */
  #orderedActivePackages(benefitType: string, at: number): PackageState[] {
    const result: PackageState[] = [];
    for (const pkg of this.#packages.values()) {
      if (pkg.current.grants[benefitType] === undefined) continue;
      if (!isActive(pkg.current.interval, at)) continue;
      result.push(pkg);
    }
    result.sort((a, b) =>
      comparePackageKeys(
        a.current.interval.validFrom, a.current.interval.validTo, a.id,
        b.current.interval.validFrom, b.current.interval.validTo, b.id,
      ),
    );
    return result;
  }

  #requireRule(benefitType: string): MergeRule {
    const rule = this.#mergeRules.get(benefitType);
    if (rule === undefined) {
      throw new UnknownBenefitTypeError(`未注册的权益类型：${benefitType}`);
    }
    return rule;
  }

  #requirePackage(packageId: string): PackageState {
    const pkg = this.#packages.get(packageId);
    if (!pkg) {
      throw new UnknownPackageError(`套餐不存在：${packageId}`);
    }
    return pkg;
  }

  #snapshot(record: ConsumptionRecord): ConsumptionRecord {
    return { ...record, allocations: Object.freeze([...record.allocations]) };
  }
}

function comparePackageKeys(
  aFrom: number, aTo: number, aId: string,
  bFrom: number, bTo: number, bId: string,
): number {
  return aTo - bTo || aFrom - bFrom || (aId < bId ? -1 : aId > bId ? 1 : 0);
}

function comparePackageBalance(a: PackageBalance, b: PackageBalance): number {
  return comparePackageKeys(
    a.interval.validFrom, a.interval.validTo, a.packageId,
    b.interval.validFrom, b.interval.validTo, b.packageId,
  );
}

import type {
  Allocation,
  AllocationConflict,
  BalanceView,
  BenefitRule,
  ConsumptionRecord,
  LedgerEvent,
  PackageBalance,
  PackageCorrection,
  PackageInput,
} from './types.ts';

export class LedgerError extends Error {}
export class InsufficientQuotaError extends LedgerError {}
export class UnknownBenefitTypeError extends LedgerError {}
export class UnknownPackageError extends LedgerError {}
export class DuplicatePackageError extends LedgerError {}
export class UnknownConsumptionError extends LedgerError {}
export class AlreadyRevokedError extends LedgerError {}
export class InvalidInputError extends LedgerError {}

interface MemberPackage {
  current: PackageInput;
}

function assertPositiveInt(value: number, name: string): void {
  if (!Number.isInteger(value) || value <= 0) {
    throw new InvalidInputError(`${name} must be a positive integer, got ${value}`);
  }
}

function assertNonNegativeInt(value: number, name: string): void {
  if (!Number.isInteger(value) || value < 0) {
    throw new InvalidInputError(`${name} must be a non-negative integer, got ${value}`);
  }
}

function clonePackage(input: PackageInput): PackageInput {
  return { packageId: input.packageId, validFrom: input.validFrom, validTo: input.validTo, quotas: { ...input.quotas } };
}

function cloneRecord(record: ConsumptionRecord): ConsumptionRecord {
  return {
    ...record,
    allocations: record.allocations.map((a) => ({ ...a })),
  };
}

/**
 * 会员权益账本。
 *
 * 不变量：
 * - 已确认消费的 allocations 一经写入永不修改；余额一律由消费日志推导，
 *   因此套餐追溯更正只会影响后续余额计算，不会悄悄改写历史分配。
 * - 撤销按原 allocations 逐笔返还到来源套餐，套餐有效期保持不变，
 *   已过期的套餐即使被返还额度也无法再被消费。
 * - 扣减顺序完全由 (有效期, 套餐ID, 规则) 决定，同样的操作序列必然得到同样的分配。
 */
export class BenefitLedger {
  private readonly rules = new Map<string, BenefitRule>();
  private readonly members = new Map<string, Map<string, MemberPackage>>();
  private readonly consumptions: ConsumptionRecord[] = [];
  private readonly events: LedgerEvent[] = [];
  private seq = 0;

  registerBenefitType(benefitType: string, rule: BenefitRule): void {
    if (rule !== 'stack' && rule !== 'max') {
      throw new InvalidInputError(`unknown benefit rule: ${rule}`);
    }
    const existing = this.rules.get(benefitType);
    if (existing !== undefined) {
      if (existing !== rule) {
        throw new InvalidInputError(
          `benefit type ${benefitType} already registered with rule ${existing}, cannot change to ${rule}`,
        );
      }
      return;
    }
    this.rules.set(benefitType, rule);
    this.events.push({ seq: ++this.seq, kind: 'benefit_type_registered', benefitType, rule });
  }

  addPackage(memberId: string, input: PackageInput): void {
    if (!input.packageId) throw new InvalidInputError('packageId is required');
    if (!(input.validFrom < input.validTo)) {
      throw new InvalidInputError(`package ${input.packageId}: validFrom must be < validTo`);
    }
    for (const [benefitType, quota] of Object.entries(input.quotas)) {
      if (!this.rules.has(benefitType)) throw new UnknownBenefitTypeError(benefitType);
      assertNonNegativeInt(quota, `quota[${benefitType}]`);
    }
    const packages = this.packagesOf(memberId);
    if (packages.has(input.packageId)) throw new DuplicatePackageError(input.packageId);
    packages.set(input.packageId, { current: clonePackage(input) });
    this.events.push({ seq: ++this.seq, kind: 'package_added', memberId, packageId: input.packageId, input: clonePackage(input) });
  }

  /**
   * 追溯更正套餐。只更新套餐的当前版本，已确认消费的分配保持不变；
   * 若更正后与历史消费矛盾，可通过 detectConflicts() 显式查出。
   */
  correctPackage(memberId: string, packageId: string, correction: PackageCorrection, correctedAt: number): void {
    const pkg = this.mustGetPackage(memberId, packageId);
    const before = clonePackage(pkg.current);
    const after = clonePackage(pkg.current);
    if (correction.validFrom !== undefined) after.validFrom = correction.validFrom;
    if (correction.validTo !== undefined) after.validTo = correction.validTo;
    if (correction.quotas !== undefined) {
      for (const [benefitType, quota] of Object.entries(correction.quotas)) {
        if (!this.rules.has(benefitType)) throw new UnknownBenefitTypeError(benefitType);
        assertNonNegativeInt(quota, `quota[${benefitType}]`);
        after.quotas[benefitType] = quota;
      }
    }
    if (!(after.validFrom < after.validTo)) {
      throw new InvalidInputError(`package ${packageId}: corrected validFrom must be < validTo`);
    }
    pkg.current = after;
    this.events.push({
      seq: ++this.seq,
      kind: 'package_corrected',
      memberId,
      packageId,
      before,
      after: clonePackage(after),
      correctedAt,
    });
  }

  /**
   * 确定性扣减：
   * - stack：按 (validTo 升序, packageId 升序) 从各套餐剩余额度中依次扣减（先到期的先用）。
   * - max：等效额度为各套餐剩余的最大值，扣减全部落在剩余最多的套餐上
   *   （并列时按 validTo、packageId 升序），保证总额度不被突破。
   */
  consume(memberId: string, benefitType: string, amount: number, at: number): ConsumptionRecord {
    const rule = this.mustGetRule(benefitType);
    assertPositiveInt(amount, 'amount');

    const candidates: PackageBalance[] = this.balancesOf(memberId, benefitType, at).filter((p) => p.valid && p.remaining > 0);
    const effective = rule === 'stack'
      ? candidates.reduce((sum, p) => sum + p.remaining, 0)
      : candidates.reduce((best, p) => Math.max(best, p.remaining), 0);
    if (amount > effective) {
      throw new InsufficientQuotaError(
        `member ${memberId} benefit ${benefitType}: requested ${amount}, available ${effective}`,
      );
    }

    const ordered = [...candidates].sort((a, b) =>
      rule === 'stack'
        ? a.validTo - b.validTo || a.packageId.localeCompare(b.packageId)
        : b.remaining - a.remaining || a.validTo - b.validTo || a.packageId.localeCompare(b.packageId),
    );

    const allocations: Allocation[] = [];
    let needed = amount;
    for (const candidate of ordered) {
      if (needed === 0) break;
      const take = Math.min(candidate.remaining, needed);
      allocations.push({ packageId: candidate.packageId, amount: take });
      needed -= take;
    }
    if (needed !== 0) throw new LedgerError(`internal: allocation shortfall ${needed}`);

    const record: ConsumptionRecord = {
      consumptionId: `c${this.seq + 1}`,
      memberId,
      benefitType,
      amount,
      consumedAt: at,
      allocations,
      status: 'confirmed',
      revokedAt: null,
    };
    this.consumptions.push(record);
    this.events.push({ seq: ++this.seq, kind: 'consumption_confirmed', record: cloneRecord(record) });
    return cloneRecord(record);
  }

  /** 撤销消费：按原 allocations 返还来源套餐，不延长任何套餐的有效期。 */
  revoke(consumptionId: string, revokedAt: number): ConsumptionRecord {
    const record = this.consumptions.find((r) => r.consumptionId === consumptionId);
    if (!record) throw new UnknownConsumptionError(consumptionId);
    if (record.status === 'revoked') throw new AlreadyRevokedError(consumptionId);
    record.status = 'revoked';
    record.revokedAt = revokedAt;
    this.events.push({ seq: ++this.seq, kind: 'consumption_revoked', memberId: record.memberId, consumptionId, revokedAt });
    return cloneRecord(record);
  }

  /** 查询某时刻某类权益的可用额度与各套餐明细。 */
  getBalance(memberId: string, benefitType: string, at: number): BalanceView {
    const rule = this.mustGetRule(benefitType);
    const packages = this.balancesOf(memberId, benefitType, at);
    const valid = packages.filter((p) => p.valid);
    const effective = rule === 'stack'
      ? valid.reduce((sum, p) => sum + p.remaining, 0)
      : valid.reduce((best, p) => Math.max(best, p.remaining), 0);
    return { memberId, benefitType, rule, at, effective, packages };
  }

  /** 消费历史查询。返回深拷贝，调用方无法借此篡改账本。 */
  getConsumptionHistory(filter: { memberId?: string; benefitType?: string; includeRevoked?: boolean } = {}): ConsumptionRecord[] {
    const includeRevoked = filter.includeRevoked ?? true;
    return this.consumptions
      .filter((r) => (filter.memberId === undefined || r.memberId === filter.memberId)
        && (filter.benefitType === undefined || r.benefitType === filter.benefitType)
        && (includeRevoked || r.status === 'confirmed'))
      .map(cloneRecord);
  }

  /** 全量审计事件（含套餐更正前后快照），按发生顺序返回。 */
  getEvents(): LedgerEvent[] {
    return this.events.map((e) => {
      if (e.kind === 'package_added') return { ...e, input: clonePackage(e.input) };
      if (e.kind === 'package_corrected') return { ...e, before: clonePackage(e.before), after: clonePackage(e.after) };
      if (e.kind === 'consumption_confirmed') return { ...e, record: cloneRecord(e.record) };
      return { ...e };
    });
  }

  /**
   * 校验已确认消费的分配是否仍与套餐当前版本一致。
   * 追溯更正不会改写历史，但可能产生冲突；此处只报告，由调用方决定如何处理。
   */
  detectConflicts(memberId?: string): AllocationConflict[] {
    const conflicts: AllocationConflict[] = [];
    for (const record of this.consumptions) {
      if (record.status !== 'confirmed') continue;
      if (memberId !== undefined && record.memberId !== memberId) continue;
      for (const allocation of record.allocations) {
        const pkg = this.members.get(record.memberId)?.get(allocation.packageId);
        if (!pkg) {
          conflicts.push({
            consumptionId: record.consumptionId,
            packageId: allocation.packageId,
            benefitType: record.benefitType,
            reason: 'package_missing',
            detail: `package ${allocation.packageId} no longer exists for member ${record.memberId}`,
          });
          continue;
        }
        const current = pkg.current;
        const quota = current.quotas[record.benefitType];
        if (quota === undefined) {
          conflicts.push({
            consumptionId: record.consumptionId,
            packageId: allocation.packageId,
            benefitType: record.benefitType,
            reason: 'quota_missing',
            detail: `package ${allocation.packageId} no longer grants ${record.benefitType}`,
          });
          continue;
        }
        if (!(current.validFrom <= record.consumedAt && record.consumedAt < current.validTo)) {
          conflicts.push({
            consumptionId: record.consumptionId,
            packageId: allocation.packageId,
            benefitType: record.benefitType,
            reason: 'not_valid_at_consumption',
            detail: `package ${allocation.packageId} corrected to [${current.validFrom}, ${current.validTo}), `
              + `but consumption happened at ${record.consumedAt}`,
          });
        }
        const consumed = this.consumedOn(record.memberId, allocation.packageId, record.benefitType);
        if (consumed > quota) {
          conflicts.push({
            consumptionId: record.consumptionId,
            packageId: allocation.packageId,
            benefitType: record.benefitType,
            reason: 'quota_below_consumed',
            detail: `package ${allocation.packageId} quota corrected to ${quota}, `
              + `below confirmed consumption ${consumed}`,
          });
        }
      }
    }
    return conflicts;
  }

  private packagesOf(memberId: string): Map<string, MemberPackage> {
    let packages = this.members.get(memberId);
    if (!packages) {
      packages = new Map();
      this.members.set(memberId, packages);
    }
    return packages;
  }

  private mustGetRule(benefitType: string): BenefitRule {
    const rule = this.rules.get(benefitType);
    if (rule === undefined) throw new UnknownBenefitTypeError(benefitType);
    return rule;
  }

  private mustGetPackage(memberId: string, packageId: string): MemberPackage {
    const pkg = this.members.get(memberId)?.get(packageId);
    if (!pkg) throw new UnknownPackageError(`member ${memberId} package ${packageId}`);
    return pkg;
  }

  /** 某套餐某类权益已确认消费的总量（由消费日志推导，撤销自动冲减）。 */
  private consumedOn(memberId: string, packageId: string, benefitType: string): number {
    let total = 0;
    for (const record of this.consumptions) {
      if (record.memberId !== memberId || record.benefitType !== benefitType || record.status !== 'confirmed') continue;
      for (const allocation of record.allocations) {
        if (allocation.packageId === packageId) total += allocation.amount;
      }
    }
    return total;
  }

  private balancesOf(memberId: string, benefitType: string, at: number): PackageBalance[] {
    const packages = this.members.get(memberId);
    if (!packages) return [];
    const result: PackageBalance[] = [];
    for (const [packageId, pkg] of packages) {
      const quota = pkg.current.quotas[benefitType];
      if (quota === undefined) continue;
      const consumed = this.consumedOn(memberId, packageId, benefitType);
      result.push({
        packageId,
        quota,
        consumed,
        remaining: Math.max(0, quota - consumed),
        validFrom: pkg.current.validFrom,
        validTo: pkg.current.validTo,
        valid: pkg.current.validFrom <= at && at < pkg.current.validTo,
      });
    }
    return result.sort((a, b) => a.packageId.localeCompare(b.packageId));
  }
}

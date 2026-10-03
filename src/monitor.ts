/**
 * 多币种联合授信时态敞口监控器。
 *
 * 集团授信额度与子公司敞口双时态监控：
 * - 授信协议有效期（valid time）决定子公司敞口是否计入集团总敞口；
 * - 汇率（双时态记录）决定外币敞口在任意历史时点的折算结果；
 * - 担保物价值变动触发质押率重算，重算结果留痕可追溯。
 */
import { BitemporalStore, FAR_FUTURE, latest } from './bitemporal.ts';

export interface CreditAgreement {
  subsidiaryId: string;
  limit: number;
  currency: string;
}

export interface Drawdown {
  subsidiaryId: string;
  amount: number;
  currency: string;
}

export interface FxRate {
  from: string;
  to: string;
  rate: number;
}

export interface CollateralValuation {
  collateralId: string;
  subsidiaryId: string;
  value: number;
  currency: string;
}

export interface GroupLimit {
  limit: number;
  currency: string;
  warningRatio: number;
}

export type AlertKind = 'WARNING_BREACH' | 'LIMIT_BREACH' | 'PLEDGE_RATIO_BREACH';

export interface Alert {
  kind: AlertKind;
  validTime: number;
  txTime: number;
  message: string;
}

export interface ExposureBreakdownItem {
  subsidiaryId: string;
  amount: number;
  currency: string;
  fxRate: number;
  converted: number;
}

export interface ExposureReport {
  validTime: number;
  txTime: number;
  groupCurrency: string;
  groupLimit: number;
  warningLine: number;
  totalExposure: number;
  utilization: number;
  breached: boolean;
  breakdown: ExposureBreakdownItem[];
  alerts: Alert[];
}

export interface PledgeRecalcEntry {
  txTime: number;
  validTime: number;
  reason: 'collateral-revaluation' | 'drawdown-correction';
  subsidiaryId: string;
  outstanding: number;
  collateralValue: number;
  pledgeRatio: number | null;
  breached: boolean;
}

const round2 = (n: number): number => Math.round(n * 100) / 100;

export class ExposureMonitor {
  /** 质押率上限（敞口 / 担保物价值），超过即告警。 */
  readonly maxPledgeRatio: number;

  private agreements = new BitemporalStore<CreditAgreement>();
  private drawdowns = new BitemporalStore<Drawdown>();
  private fxRates = new BitemporalStore<FxRate>();
  private collaterals = new BitemporalStore<CollateralValuation>();
  private groupLimits = new BitemporalStore<GroupLimit>();

  /** 质押率重算留痕（审计追溯）。 */
  readonly pledgeTrace: PledgeRecalcEntry[] = [];
  /** 全部告警流水。 */
  readonly alertLog: Alert[] = [];

  constructor(maxPledgeRatio = 0.8) {
    this.maxPledgeRatio = maxPledgeRatio;
  }

  setGroupLimit(limit: GroupLimit, validFrom: number, txTime: number): void {
    this.groupLimits.insert(limit, validFrom, txTime);
  }

  /** 登记子公司授信协议，validFrom/validTo 为协议有效期。 */
  addAgreement(agreement: CreditAgreement, validFrom: number, txTime: number, validTo: number = FAR_FUTURE): void {
    this.agreements.insert(agreement, validFrom, txTime, validTo);
  }

  recordDrawdown(drawdown: Drawdown, validFrom: number, txTime: number): void {
    this.drawdowns.insert(drawdown, validFrom, txTime);
  }

  /**
   * 追溯修正子公司历史借款：新金额自原借款生效时间起生效，
   * 修正后自动重算该子公司质押率并留痕。
   */
  correctDrawdown(drawdown: Drawdown, txTime: number): void {
    this.drawdowns.correct((d) => d.subsidiaryId === drawdown.subsidiaryId, drawdown, txTime);
    this.recalcPledge(drawdown.subsidiaryId, txTime, txTime, 'drawdown-correction');
  }

  recordFxRate(rate: FxRate, validFrom: number, txTime: number): void {
    this.fxRates.insert(rate, validFrom, txTime);
  }

  /** 担保物重估值：同一担保物的新估值自 validFrom 起接续旧估值，并立即触发质押率重算留痕。 */
  revalueCollateral(valuation: CollateralValuation, validFrom: number, txTime: number): void {
    this.collaterals.truncateValid((c) => c.collateralId === valuation.collateralId, validFrom);
    this.collaterals.insert(valuation, validFrom, txTime);
    this.recalcPledge(valuation.subsidiaryId, validFrom, txTime, 'collateral-revaluation');
  }

  /** 查询 (vt, tt) 时点 from→to 的汇率；同币种返回 1。 */
  fxRateAt(from: string, to: string, validTime: number, txTime: number): number {
    if (from === to) return 1;
    const record = latest(this.fxRates.asOf(validTime, txTime).filter((r) => r.payload.from === from && r.payload.to === to));
    if (!record) throw new Error(`缺少汇率 ${from}->${to} @vt=${validTime}`);
    return record.payload.rate;
  }

  private groupLimitAt(validTime: number, txTime: number): GroupLimit {
    const record = latest(this.groupLimits.asOf(validTime, txTime));
    if (!record) throw new Error(`缺少集团授信额度 @vt=${validTime}`);
    return record.payload;
  }

  /** 子公司在 (vt, tt) 的未偿敞口（折算为集团币种）。 */
  subsidiaryExposureAt(subsidiaryId: string, validTime: number, txTime: number): number {
    const groupCurrency = this.groupLimitAt(validTime, txTime).currency;
    let total = 0;
    for (const record of this.drawdowns.asOf(validTime, txTime)) {
      const d = record.payload;
      if (d.subsidiaryId !== subsidiaryId) continue;
      total += d.amount * this.fxRateAt(d.currency, groupCurrency, validTime, txTime);
    }
    return total;
  }

  /** 集团总敞口：仅计入 (vt, tt) 时点授信协议仍处有效期内的子公司。 */
  exposureAt(validTime: number, txTime: number): ExposureReport {
    const group = this.groupLimitAt(validTime, txTime);
    const active = new Set(this.agreements.asOf(validTime, txTime).map((r) => r.payload.subsidiaryId));
    const breakdown: ExposureBreakdownItem[] = [];
    let total = 0;
    for (const record of this.drawdowns.asOf(validTime, txTime)) {
      const d = record.payload;
      if (!active.has(d.subsidiaryId)) continue;
      const rate = this.fxRateAt(d.currency, group.currency, validTime, txTime);
      const converted = d.amount * rate;
      total += converted;
      breakdown.push({
        subsidiaryId: d.subsidiaryId,
        amount: d.amount,
        currency: d.currency,
        fxRate: rate,
        converted: round2(converted),
      });
    }
    const warningLine = round2(group.limit * group.warningRatio);
    const totalExposure = round2(total);
    const breached = totalExposure >= warningLine;
    const alerts: Alert[] = [];
    if (totalExposure >= group.limit) {
      alerts.push({
        kind: 'LIMIT_BREACH',
        validTime,
        txTime,
        message: `集团总敞口 ${totalExposure} ${group.currency} 已击穿授信上限 ${group.limit}`,
      });
    } else if (breached) {
      alerts.push({
        kind: 'WARNING_BREACH',
        validTime,
        txTime,
        message: `集团总敞口 ${totalExposure} ${group.currency} 触碰预警线 ${warningLine}（${group.warningRatio * 100}% of ${group.limit}）`,
      });
    }
    return {
      validTime,
      txTime,
      groupCurrency: group.currency,
      groupLimit: group.limit,
      warningLine,
      totalExposure,
      utilization: round2(total / group.limit),
      breached,
      breakdown,
      alerts,
    };
  }

  /** 监控入口：评估 (vt, tt) 时点集团敞口，告警写入流水并返回报告。 */
  check(validTime: number, txTime: number): ExposureReport {
    const report = this.exposureAt(validTime, txTime);
    this.alertLog.push(...report.alerts);
    return report;
  }

  /** 子公司质押率：未偿敞口 / 担保物价值（均折算为集团币种）。 */
  pledgeRatioAt(subsidiaryId: string, validTime: number, txTime: number): number | null {
    const collateralValue = this.collateralValueAt(subsidiaryId, validTime, txTime);
    if (collateralValue <= 0) return null;
    return this.subsidiaryExposureAt(subsidiaryId, validTime, txTime) / collateralValue;
  }

  private collateralValueAt(subsidiaryId: string, validTime: number, txTime: number): number {
    const groupCurrency = this.groupLimitAt(validTime, txTime).currency;
    let total = 0;
    for (const record of this.collaterals.asOf(validTime, txTime)) {
      const c = record.payload;
      if (c.subsidiaryId !== subsidiaryId) continue;
      total += c.value * this.fxRateAt(c.currency, groupCurrency, validTime, txTime);
    }
    return total;
  }

  /** 质押率重算并留痕；超过上限时产生 PLEDGE_RATIO_BREACH 告警。 */
  private recalcPledge(
    subsidiaryId: string,
    validTime: number,
    txTime: number,
    reason: PledgeRecalcEntry['reason'],
  ): void {
    const outstanding = round2(this.subsidiaryExposureAt(subsidiaryId, validTime, txTime));
    const collateralValue = round2(this.collateralValueAt(subsidiaryId, validTime, txTime));
    const ratio = collateralValue > 0 ? round2(outstanding / collateralValue) : null;
    const breached = ratio !== null && ratio > this.maxPledgeRatio;
    this.pledgeTrace.push({
      txTime,
      validTime,
      reason,
      subsidiaryId,
      outstanding,
      collateralValue,
      pledgeRatio: ratio,
      breached,
    });
    if (breached) {
      this.alertLog.push({
        kind: 'PLEDGE_RATIO_BREACH',
        validTime,
        txTime,
        message: `子公司 ${subsidiaryId} 质押率 ${(ratio! * 100).toFixed(1)}% 超过上限 ${this.maxPledgeRatio * 100}%（敞口 ${outstanding} / 担保物 ${collateralValue}）`,
      });
    }
  }

  /** 借款历史（含被修正关闭的记录），用于审计。 */
  drawdownHistory() {
    return this.drawdowns.history();
  }
}

import { BitemporalStore } from './bitemporal.ts';
import type { CreditLine, Loan, FxRate, Collateral } from './types.ts';

export type AlertCode =
  | 'WARNING_LINE_BREACHED'
  | 'AGREEMENT_INACTIVE'
  | 'FX_RATE_MISSING'
  | 'PLEDGE_RATIO_EXCEEDED';

export interface Alert {
  readonly code: AlertCode;
  readonly message: string;
}

/** 单笔子公司敞口折算结果。 */
export interface ExposureItem {
  readonly subsidiary: string;
  readonly amount: number;
  readonly currency: string;
  readonly fxRate: number | null;
  /** 折算为基准币种后的敞口；汇率缺失时为 null 且不计入总敞口。 */
  readonly converted: number | null;
}

/** 质押率评估快照。 */
export interface PledgeEvaluation {
  readonly owner: string;
  readonly loanAmount: number;
  readonly collateralValue: number;
  readonly pledgeRatio: number;
}

/** 质押率重算追溯记录：每次评估发现质押率发生变化时追加。 */
export interface PledgeTraceEntry extends PledgeEvaluation {
  readonly validTime: number;
  readonly txTime: number;
  readonly previousRatio: number | null;
  readonly causes: readonly string[];
}

/** 一次双时态评估的完整报告。 */
export interface ExposureReport {
  readonly validTime: number;
  readonly txTime: number;
  readonly baseCurrency: string;
  readonly limit: number | null;
  readonly warningLine: number | null;
  readonly items: readonly ExposureItem[];
  readonly totalExposure: number;
  /** 总敞口 / 授信额度；无有效协议时为 null。 */
  readonly utilization: number | null;
  /** 总敞口是否触碰（>=）预警线。 */
  readonly breached: boolean;
  readonly pledges: readonly PledgeEvaluation[];
  readonly alerts: readonly Alert[];
}

export interface MonitorOptions {
  readonly baseCurrency: string;
  /** 质押率上限（借款 / 担保物价值），默认 1，即借款不得超过担保物价值。 */
  readonly pledgeRatioLimit?: number;
}

const EPSILON = 1e-9;

/**
 * 多币种联合授信时态敞口监控器。
 *
 * 所有事实（授信协议、借款、汇率、担保物价值）均以双时态方式登记，
 * evaluate(validTime, txTime) 可重现任意历史时点系统所认知的集团敞口。
 */
export class ExposureMonitor {
  readonly baseCurrency: string;
  readonly pledgeRatioLimit: number;

  private readonly creditLines = new BitemporalStore<CreditLine>();
  private readonly loans = new BitemporalStore<Loan>();
  private readonly fxRates = new BitemporalStore<FxRate>();
  private readonly collaterals = new BitemporalStore<Collateral>();

  private readonly groupIds: string[] = [];
  private readonly subsidiaryIds = new Set<string>();
  private readonly collateralOwners = new Set<string>();
  private readonly pledgeTrace: PledgeTraceEntry[] = [];

  constructor(options: MonitorOptions) {
    this.baseCurrency = options.baseCurrency;
    this.pledgeRatioLimit = options.pledgeRatioLimit ?? 1;
  }

  /** 登记集团授信协议；validFrom/validTo 即协议有效期。 */
  recordCreditLine(payload: CreditLine, validFrom: number, validTo: number, recordedAt: number): void {
    const key = `creditline:${payload.groupId}`;
    if (!this.groupIds.includes(payload.groupId)) this.groupIds.push(payload.groupId);
    this.creditLines.record({ key, validFrom, validTo, recordedAt, payload });
  }

  /** 登记子公司借款；追溯修正历史借款时以相同的 validFrom、更晚的 recordedAt 再次登记。 */
  recordLoan(payload: Loan, validFrom: number, validTo: number, recordedAt: number): void {
    this.subsidiaryIds.add(payload.subsidiary);
    this.loans.record({ key: `loan:${payload.subsidiary}`, validFrom, validTo, recordedAt, payload });
  }

  /** 登记汇率；validFrom 为汇率生效时间，有效期至下一条同币种对汇率生效。 */
  recordFxRate(payload: FxRate, validFrom: number, recordedAt: number, validTo = Infinity): void {
    this.fxRates.record({ key: `fx:${payload.from}->${payload.to}`, validFrom, validTo, recordedAt, payload });
  }

  /** 登记担保物价值；价值变动（重估）时以新的 validFrom 再次登记。 */
  recordCollateral(payload: Collateral, validFrom: number, recordedAt: number, validTo = Infinity): void {
    this.collateralOwners.add(payload.owner);
    this.collaterals.record({ key: `collateral:${payload.owner}`, validFrom, validTo, recordedAt, payload });
  }

  /** 质押率重算追溯日志（按评估发生顺序追加）。 */
  getPledgeTrace(): readonly PledgeTraceEntry[] {
    return this.pledgeTrace;
  }

  /**
   * 重现 (validTime, txTime) 时点的集团整体敞口并校验预警线。
   * 汇率取 validTime 时点生效的汇率，事实取 txTime 时点系统已知的版本。
   */
  evaluate(validTime: number, txTime: number): ExposureReport {
    const alerts: Alert[] = [];

    let limit: number | null = null;
    let warningLine: number | null = null;
    for (const groupId of this.groupIds) {
      const rec = this.creditLines.asOf(`creditline:${groupId}`, validTime, txTime);
      if (rec === undefined) {
        alerts.push({
          code: 'AGREEMENT_INACTIVE',
          message: `集团 ${groupId} 在 validTime=${validTime} 无生效中的授信协议（协议未生效或已过期）`,
        });
        continue;
      }
      const { limitAmount, currency, warningRatio } = rec.payload;
      const convertedLimit = this.convert(limitAmount, currency, validTime, txTime, alerts);
      if (convertedLimit !== null) {
        limit = (limit ?? 0) + convertedLimit;
        warningLine = (warningLine ?? 0) + convertedLimit * warningRatio;
      }
    }

    const items: ExposureItem[] = [];
    let totalExposure = 0;
    for (const subsidiary of [...this.subsidiaryIds].sort()) {
      const rec = this.loans.asOf(`loan:${subsidiary}`, validTime, txTime);
      if (rec === undefined) continue;
      const { amount, currency } = rec.payload;
      const rate = this.lookupRate(currency, validTime, txTime);
      const converted = this.convert(amount, currency, validTime, txTime, alerts);
      if (converted !== null) totalExposure += converted;
      items.push({ subsidiary, amount, currency, fxRate: rate, converted });
    }

    const breached = warningLine !== null && totalExposure >= warningLine - EPSILON;
    if (breached && warningLine !== null && limit !== null) {
      alerts.push({
        code: 'WARNING_LINE_BREACHED',
        message:
          `集团总敞口 ${totalExposure.toFixed(2)} ${this.baseCurrency} 触碰预警线 ` +
          `${warningLine.toFixed(2)}（授信额度 ${limit.toFixed(2)}）`,
      });
    }

    const pledges = this.evaluatePledges(validTime, txTime, alerts);

    return {
      validTime,
      txTime,
      baseCurrency: this.baseCurrency,
      limit,
      warningLine,
      items,
      totalExposure,
      utilization: limit !== null && limit > 0 ? totalExposure / limit : null,
      breached,
      pledges,
      alerts,
    };
  }

  /** 评估各担保人的质押率；较上次评估发生变化时追加追溯记录。 */
  private evaluatePledges(validTime: number, txTime: number, alerts: Alert[]): PledgeEvaluation[] {
    const result: PledgeEvaluation[] = [];
    for (const owner of [...this.collateralOwners].sort()) {
      const collateralRec = this.collaterals.asOf(`collateral:${owner}`, validTime, txTime);
      const loanRec = this.loans.asOf(`loan:${owner}`, validTime, txTime);
      if (collateralRec === undefined || loanRec === undefined) continue;
      const collateralValue = this.convert(
        collateralRec.payload.value,
        collateralRec.payload.currency,
        validTime,
        txTime,
        alerts,
      );
      const loanAmount = this.convert(loanRec.payload.amount, loanRec.payload.currency, validTime, txTime, alerts);
      if (collateralValue === null || loanAmount === null || collateralValue <= 0) continue;
      const pledgeRatio = loanAmount / collateralValue;
      const evaluation: PledgeEvaluation = { owner, loanAmount, collateralValue, pledgeRatio };
      result.push(evaluation);

      const previous = [...this.pledgeTrace].reverse().find((entry) => entry.owner === owner);
      const changed =
        previous === undefined || Math.abs(previous.pledgeRatio - pledgeRatio) > EPSILON;
      if (changed) {
        const causes: string[] = [];
        if (previous === undefined) {
          causes.push('首次评估');
        } else {
          if (Math.abs(previous.loanAmount - loanAmount) > EPSILON) causes.push('借款敞口变动');
          if (Math.abs(previous.collateralValue - collateralValue) > EPSILON) {
            causes.push('担保物价值变动');
          }
          if (causes.length === 0) causes.push('汇率变动');
        }
        this.pledgeTrace.push({
          ...evaluation,
          validTime,
          txTime,
          previousRatio: previous?.pledgeRatio ?? null,
          causes,
        });
      }

      if (pledgeRatio > this.pledgeRatioLimit + EPSILON) {
        alerts.push({
          code: 'PLEDGE_RATIO_EXCEEDED',
          message:
            `${owner} 质押率 ${(pledgeRatio * 100).toFixed(2)}% 超过上限 ` +
            `${(this.pledgeRatioLimit * 100).toFixed(2)}%（借款 ${loanAmount.toFixed(2)} / 担保物 ${collateralValue.toFixed(2)} ${this.baseCurrency}）`,
        });
      }
    }
    return result;
  }

  private lookupRate(currency: string, validTime: number, txTime: number): number | null {
    if (currency === this.baseCurrency) return 1;
    const direct = this.fxRates.asOf(`fx:${currency}->${this.baseCurrency}`, validTime, txTime);
    if (direct !== undefined) return direct.payload.rate;
    const inverse = this.fxRates.asOf(`fx:${this.baseCurrency}->${currency}`, validTime, txTime);
    if (inverse !== undefined) return 1 / inverse.payload.rate;
    return null;
  }

  private convert(
    amount: number,
    currency: string,
    validTime: number,
    txTime: number,
    alerts: Alert[],
  ): number | null {
    const rate = this.lookupRate(currency, validTime, txTime);
    if (rate === null) {
      alerts.push({
        code: 'FX_RATE_MISSING',
        message: `缺少 ${currency}->${this.baseCurrency} 在 validTime=${validTime} 的汇率，相关金额未计入总敞口`,
      });
      return null;
    }
    return amount * rate;
  }
}

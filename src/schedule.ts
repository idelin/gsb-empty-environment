import { parseRate, formatRate, type RationalRate, type RateInput } from './rate.ts';
import { annuityPayment, interestOf } from './money.ts';

/** 提前还款方式：缩期（月供不变、缩短剩余期数）或减额（期数不变、重算月供）。 */
export type PrepayMode = 'shorten' | 'reduce';

export interface RateChange {
  /** 自第几期起生效（1 起计，含该期） */
  readonly fromPeriod: number;
  readonly rate: RateInput;
}

export interface Prepayment {
  /** 在第几期随当期月供一并归还（1 起计） */
  readonly period: number;
  /** 提前还款金额（分），全部冲抵本金 */
  readonly amount: number;
  readonly mode: PrepayMode;
}

export interface LoanInput {
  /** 本金（分），正整数 */
  readonly principal: number;
  /** 总期数 */
  readonly periods: number;
  /** 月费率，十进制表示，如 "0.005" 表示月息 0.5% */
  readonly monthlyRate: RateInput;
  /** 费率变更，按 fromPeriod 升序 */
  readonly rateChanges?: readonly RateChange[];
  /** 提前还款计划 */
  readonly prepayments?: readonly Prepayment[];
}

export interface ScheduleRow {
  /** 期次（1 起计） */
  readonly period: number;
  /** 当期适用月费率（十进制字符串） */
  readonly rate: string;
  /** 当期应还总额（分）= 利息 + 本金部分（不含提前还款） */
  readonly payment: number;
  /** 当期利息（分），月初按上期末剩余本金计息 */
  readonly interest: number;
  /** 当期月供中冲抵本金的部分（分） */
  readonly principal: number;
  /** 当期提前还款冲抵本金（分） */
  readonly prepaidPrincipal: number;
  /** 本期结束后剩余本金（分） */
  readonly balance: number;
}

export interface Schedule {
  readonly rows: readonly ScheduleRow[];
  /** 实际总期数（缩期后可能小于原期数） */
  readonly totalPeriods: number;
  /** 累计利息（分） */
  readonly totalInterest: number;
  /** 累计还款总额（分，含提前还款） */
  readonly totalPaid: number;
}

interface NormalizedInput {
  principal: number;
  periods: number;
  rate: RationalRate;
  rateChanges: Map<number, RationalRate>;
  prepayments: Map<number, Prepayment>;
}

function assertPositiveInteger(value: number, name: string): void {
  if (!Number.isInteger(value) || value <= 0) {
    throw new Error(`${name} 必须为正整数（单位：分），收到 ${value}`);
  }
}

function normalize(input: LoanInput): NormalizedInput {
  assertPositiveInteger(input.principal, '本金');
  assertPositiveInteger(input.periods, '期数');
  const rate = parseRate(input.monthlyRate);

  const rateChanges = new Map<number, RationalRate>();
  for (const change of input.rateChanges ?? []) {
    if (!Number.isInteger(change.fromPeriod) || change.fromPeriod < 1 || change.fromPeriod > input.periods) {
      throw new Error(`费率变更期次无效: ${change.fromPeriod}`);
    }
    rateChanges.set(change.fromPeriod, parseRate(change.rate));
  }

  const prepayments = new Map<number, Prepayment>();
  for (const prepay of input.prepayments ?? []) {
    if (!Number.isInteger(prepay.period) || prepay.period < 1 || prepay.period > input.periods) {
      throw new Error(`提前还款期次无效: ${prepay.period}`);
    }
    assertPositiveInteger(prepay.amount, '提前还款金额');
    if (prepayments.has(prepay.period)) {
      throw new Error(`第 ${prepay.period} 期存在多笔提前还款`);
    }
    prepayments.set(prepay.period, prepay);
  }

  return { principal: input.principal, periods: input.periods, rate, rateChanges, prepayments };
}

/**
 * 生成等额本息还款计划。
 * 规则：月初计息、按分舍入（half-up）、月供先息后本；
 * 最后一期月供 = 剩余本金 + 当期利息，自动吸收累计尾差。
 */
export function generateSchedule(input: LoanInput): Schedule {
  const { principal, periods, rate: initialRate, rateChanges, prepayments } = normalize(input);
  let rate: RationalRate = rateChanges.get(1) ?? initialRate;

  const rows: ScheduleRow[] = [];
  let balance = principal;
  let payment = annuityPayment(balance, periods, rate);
  let totalInterest = 0;
  let totalPaid = 0;
  let period = 0;

  while (balance > 0) {
    period += 1;
    if (period > periods) {
      // 仅当尾差方向异常时才可能走到这里，属于兜底保护
      throw new Error('还款计划超出总期数，请检查输入参数');
    }

    const changed = rateChanges.get(period);
    if (changed !== undefined && period !== 1) {
      rate = changed;
      // 费率变更：按剩余本金与剩余期数重算月供
      payment = annuityPayment(balance, periods - period + 1, rate);
    }

    const interest = interestOf(balance, rate);
    let principalPart = payment - interest;
    if (principalPart <= 0) {
      throw new Error(`第 ${period} 期月供 ${payment} 分不足以覆盖当期利息 ${interest} 分`);
    }

    let rowPayment = payment;
    if (period === periods || principalPart >= balance) {
      // 最后一期：吸收全部尾差
      principalPart = balance;
      rowPayment = balance + interest;
    }
    balance -= principalPart;

    // 提前还款：随当期月供之后全额冲抵本金
    let prepaid = 0;
    const prepay = prepayments.get(period);
    if (prepay !== undefined && balance > 0) {
      prepaid = Math.min(prepay.amount, balance);
      balance -= prepaid;
      if (balance > 0) {
        if (prepay.mode === 'reduce') {
          // 减额：期数不变，重算月供
          payment = annuityPayment(balance, periods - period, rate);
        }
        // 缩期：月供不变，循环会在本金清零时自然提前结束
      }
    }

    totalInterest += interest;
    totalPaid += rowPayment + prepaid;
    rows.push({
      period,
      rate: formatRate(rate),
      payment: rowPayment,
      interest,
      principal: principalPart,
      prepaidPrincipal: prepaid,
      balance,
    });
  }

  return { rows, totalPeriods: rows.length, totalInterest, totalPaid };
}

export interface Settlement {
  /** 结清时点：第几期月供到期前（1 起计） */
  readonly atPeriod: number;
  /** 结清时剩余本金（分） */
  readonly principal: number;
  /** 当期已计提利息（分，月初计息，结清须一并支付） */
  readonly interest: number;
  /** 结清总额（分）= 剩余本金 + 当期利息 */
  readonly payoff: number;
}

/**
 * 结清金额反算：在第 atPeriod 期月供到期前提前结清，
 * 需支付 剩余本金 + 当期利息（月初已计提）。
 */
export function settleAmount(input: LoanInput, atPeriod: number): Settlement {
  const normalized = normalize(input);
  if (!Number.isInteger(atPeriod) || atPeriod < 1 || atPeriod > normalized.periods) {
    throw new Error(`结清期次无效: ${atPeriod}`);
  }
  const schedule = generateSchedule(input);
  if (atPeriod > schedule.rows.length) {
    throw new Error(`贷款在第 ${schedule.rows.length} 期已结清，无法在第 ${atPeriod} 期结清`);
  }
  const prevBalance = atPeriod === 1 ? normalized.principal : schedule.rows[atPeriod - 2]!.balance;
  const rate = parseRate(schedule.rows[atPeriod - 1]!.rate);
  const interest = interestOf(prevBalance, rate);
  return {
    atPeriod,
    principal: prevBalance,
    interest,
    payoff: prevBalance + interest,
  };
}

import { Fraction, frac } from './fraction.ts';

export type ISODate = string;

const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

function assertISODate(date: string): void {
  if (!DATE_RE.test(date) || Number.isNaN(Date.parse(`${date}T00:00:00Z`))) {
    throw new Error(`invalid ISO date: ${date}`);
  }
}

export function nextDay(date: ISODate): ISODate {
  assertISODate(date);
  const t = Date.parse(`${date}T00:00:00Z`);
  return new Date(t + 86_400_000).toISOString().slice(0, 10);
}

export function quarterRange(label: string): { from: ISODate; to: ISODate } {
  const m = /^(\d{4})Q([1-4])$/.exec(label);
  if (!m) throw new Error(`invalid quarter label: ${label}`);
  const year = Number(m[1]);
  const q = Number(m[2]);
  const pad = (x: number): string => String(x).padStart(2, '0');
  const startMonth = (q - 1) * 3 + 1;
  const endMonth = startMonth + 3;
  const endYear = endMonth > 12 ? year + 1 : year;
  return {
    from: `${year}-${pad(startMonth)}-01`,
    to: `${endYear}-${pad(endMonth > 12 ? endMonth - 12 : endMonth)}-01`,
  };
}

export interface RevisionInput {
  seq: number;
  ruleId: string;
  validFrom: ISODate;
  validTo: ISODate; // 半开区间右端，不含当天
  weights: Record<string, Fraction>;
}

export interface ScoreInput {
  seq: number;
  date: ISODate;
  metric: string;
  value: Fraction;
}

export type LedgerEvent =
  | ({ kind: 'revision' } & RevisionInput)
  | { kind: 'revocation'; seq: number; targetSeq: number }
  | ({ kind: 'score' } & ScoreInput);

export type DayResult =
  | { date: ISODate; status: 'ok'; value: Fraction; ruleId: string; ruleSeq: number }
  | { date: ISODate; status: 'incomplete'; missing: string[]; ruleId: string; ruleSeq: number }
  | { date: ISODate; status: 'no-rule' };

export interface RuleSegment {
  from: ISODate;
  to: ISODate; // 半开区间右端
  ruleId: string | null;
  ruleSeq: number | null;
}

export interface QuarterResult {
  from: ISODate;
  to: ISODate;
  asOfSeq: number;
  days: DayResult[];
  segments: RuleSegment[];
  /** 完整得分日的加权分算术平均；没有任何完整得分日时为 null */
  total: Fraction | null;
  scoredDays: number;
  totalDays: number;
  missing: { date: ISODate; metrics: string[] }[];
  noRuleDates: ISODate[];
}

interface RevisionRecord {
  seq: number;
  ruleId: string;
  validFrom: ISODate;
  validTo: ISODate;
  weights: ReadonlyMap<string, Fraction>;
}

interface RevocationRecord {
  seq: number;
  targetSeq: number;
}

interface ScoreRecord {
  seq: number;
  date: ISODate;
  metric: string;
  value: Fraction;
}

function buildSegments(days: DayResult[]): RuleSegment[] {
  const segments: RuleSegment[] = [];
  for (const day of days) {
    const ruleId = day.status === 'no-rule' ? null : day.ruleId;
    const ruleSeq = day.status === 'no-rule' ? null : day.ruleSeq;
    const last = segments[segments.length - 1];
    if (last && last.ruleId === ruleId && last.ruleSeq === ruleSeq) {
      last.to = nextDay(day.date);
    } else {
      segments.push({ from: day.date, to: nextDay(day.date), ruleId, ruleSeq });
    }
  }
  return segments;
}

function aggregateQuarter(from: ISODate, to: ISODate, asOfSeq: number, days: DayResult[]): QuarterResult {
  let sum = frac(0);
  let scored = 0;
  const missing: { date: ISODate; metrics: string[] }[] = [];
  const noRuleDates: ISODate[] = [];
  for (const day of days) {
    if (day.status === 'ok') {
      sum = sum.add(day.value);
      scored += 1;
    } else if (day.status === 'incomplete') {
      missing.push({ date: day.date, metrics: day.missing });
    } else {
      noRuleDates.push(day.date);
    }
  }
  return {
    from,
    to,
    asOfSeq,
    days,
    segments: buildSegments(days),
    total: scored === 0 ? null : sum.div(frac(scored)),
    scoredDays: scored,
    totalDays: days.length,
    missing,
    noRuleDates,
  };
}

/**
 * 本地指标账本。
 *
 * 事件（修订 / 撤销 / 指标得分）共享全局唯一的录入序号 seq。
 * 双时点查询：asOfSeq 决定“当时能看到哪些事件”；缺省表示“按现在规则”。
 * 某日适用规则 = 覆盖该日、seq <= asOfSeq、且未被可见撤销命中的修订中 seq 最大者。
 */
export class Ledger {
  private revisions = new Map<number, RevisionRecord>();
  private revocations: RevocationRecord[] = [];
  private scores: ScoreRecord[] = [];
  private usedSeqs = new Set<number>();
  private maxSeq = 0;
  /** 日级缓存，key 为 `日期#asOf标记`，'L' 表示“按当前最新”查询 */
  private dayCache = new Map<string, DayResult>();
  readonly stats = { dayComputes: 0, cacheHits: 0 };

  currentSeq(): number {
    return this.maxSeq;
  }

  cacheSize(): number {
    return this.dayCache.size;
  }

  addRevision(input: RevisionInput): void {
    const seq = this.claimSeq(input.seq);
    assertISODate(input.validFrom);
    assertISODate(input.validTo);
    if (!(input.validFrom < input.validTo)) {
      throw new Error(`revision ${seq}: validFrom must be before validTo`);
    }
    const entries = Object.entries(input.weights);
    if (entries.length === 0) throw new Error(`revision ${seq}: weights must not be empty`);
    let sum = frac(0);
    for (const [metric, weight] of entries) {
      if (weight.isNegative()) throw new Error(`revision ${seq}: negative weight for ${metric}`);
      sum = sum.add(weight);
    }
    if (!sum.equals(frac(1))) {
      throw new Error(`revision ${seq}: weights must sum to 1, got ${sum}`);
    }
    this.revisions.set(seq, {
      seq,
      ruleId: input.ruleId,
      validFrom: input.validFrom,
      validTo: input.validTo,
      weights: new Map(entries),
    });
    this.invalidateRange(input.validFrom, input.validTo, seq);
  }

  addRevocation(seq: number, targetSeq: number): void {
    this.claimSeq(seq);
    const target = this.revisions.get(targetSeq);
    if (!target) throw new Error(`revocation ${seq}: unknown revision seq ${targetSeq}`);
    this.revocations.push({ seq, targetSeq });
    this.invalidateRange(target.validFrom, target.validTo, seq);
  }

  addScore(input: ScoreInput): void {
    const seq = this.claimSeq(input.seq);
    assertISODate(input.date);
    this.scores.push({ seq, date: input.date, metric: input.metric, value: input.value });
    this.invalidateRange(input.date, nextDay(input.date), seq);
  }

  /** 批量补录：按给定顺序应用，失效范围为各事件受影响区间的并集 */
  ingestBatch(events: LedgerEvent[]): void {
    for (const event of events) {
      if (event.kind === 'revision') this.addRevision(event);
      else if (event.kind === 'revocation') this.addRevocation(event.seq, event.targetSeq);
      else this.addScore(event);
    }
  }

  /** 增量路径：日级缓存，只重算被事件失效的日期 */
  queryQuarter(from: ISODate, to: ISODate, asOfSeq?: number): QuarterResult {
    const effective = asOfSeq ?? this.maxSeq;
    const tag = asOfSeq === undefined ? 'L' : String(asOfSeq);
    const days: DayResult[] = [];
    for (let date = from; date < to; date = nextDay(date)) {
      const key = `${date}#${tag}`;
      let day = this.dayCache.get(key);
      if (day !== undefined) {
        this.stats.cacheHits += 1;
      } else {
        day = this.computeDay(date, effective);
        this.dayCache.set(key, day);
      }
      days.push(day);
    }
    return aggregateQuarter(from, to, effective, days);
  }

  /** 独立逐日扫描：不读缓存、不共享增量路径的逐日实现，用于对照 */
  queryQuarterScan(from: ISODate, to: ISODate, asOfSeq?: number): QuarterResult {
    const effective = asOfSeq ?? this.maxSeq;
    const days: DayResult[] = [];
    for (let date = from; date < to; date = nextDay(date)) {
      let rule: RevisionRecord | null = null;
      for (const rev of this.revisions.values()) {
        if (rev.seq > effective) continue;
        if (!(rev.validFrom <= date && date < rev.validTo)) continue;
        if (this.revocations.some((r) => r.targetSeq === rev.seq && r.seq <= effective)) continue;
        if (rule === null || rev.seq > rule.seq) rule = rev;
      }
      if (rule === null) {
        days.push({ date, status: 'no-rule' });
        continue;
      }
      const missing: string[] = [];
      let total = frac(0);
      for (const [metric, weight] of rule.weights) {
        let best: ScoreRecord | null = null;
        for (const s of this.scores) {
          if (s.date === date && s.metric === metric && s.seq <= effective) {
            if (best === null || s.seq > best.seq) best = s;
          }
        }
        if (best === null) missing.push(metric);
        else total = total.add(weight.mul(best.value));
      }
      if (missing.length > 0) {
        missing.sort();
        days.push({ date, status: 'incomplete', missing, ruleId: rule.ruleId, ruleSeq: rule.seq });
      } else {
        days.push({ date, status: 'ok', value: total, ruleId: rule.ruleId, ruleSeq: rule.seq });
      }
    }
    return aggregateQuarter(from, to, effective, days);
  }

  queryQuarterLabel(label: string, asOfSeq?: number): QuarterResult {
    const { from, to } = quarterRange(label);
    return this.queryQuarter(from, to, asOfSeq);
  }

  private claimSeq(seq: number): number {
    if (!Number.isInteger(seq) || seq <= 0) {
      throw new Error(`seq must be a positive integer, got ${seq}`);
    }
    if (this.usedSeqs.has(seq)) {
      throw new Error(`duplicate entry seq ${seq}`);
    }
    this.usedSeqs.add(seq);
    if (seq > this.maxSeq) this.maxSeq = seq;
    return seq;
  }

  private isRevoked(revisionSeq: number, asOfSeq: number): boolean {
    return this.revocations.some((r) => r.targetSeq === revisionSeq && r.seq <= asOfSeq);
  }

  private resolveRule(date: ISODate, asOfSeq: number): RevisionRecord | null {
    let best: RevisionRecord | null = null;
    for (const rev of this.revisions.values()) {
      if (rev.seq > asOfSeq) continue;
      if (!(rev.validFrom <= date && date < rev.validTo)) continue;
      if (this.isRevoked(rev.seq, asOfSeq)) continue;
      if (best === null || rev.seq > best.seq) best = rev;
    }
    return best;
  }

  private latestScore(date: ISODate, metric: string, asOfSeq: number): Fraction | null {
    let bestSeq = -1;
    let value: Fraction | null = null;
    for (const s of this.scores) {
      if (s.date === date && s.metric === metric && s.seq <= asOfSeq && s.seq > bestSeq) {
        bestSeq = s.seq;
        value = s.value;
      }
    }
    return value;
  }

  private computeDay(date: ISODate, asOfSeq: number): DayResult {
    this.stats.dayComputes += 1;
    const rule = this.resolveRule(date, asOfSeq);
    if (rule === null) return { date, status: 'no-rule' };
    let total = frac(0);
    const missing: string[] = [];
    for (const [metric, weight] of rule.weights) {
      const score = this.latestScore(date, metric, asOfSeq);
      if (score === null) {
        missing.push(metric);
      } else {
        total = total.add(weight.mul(score));
      }
    }
    if (missing.length > 0) {
      missing.sort();
      return { date, status: 'incomplete', missing, ruleId: rule.ruleId, ruleSeq: rule.seq };
    }
    return { date, status: 'ok', value: total, ruleId: rule.ruleId, ruleSeq: rule.seq };
  }

  /** 只失效受影响日期区间内的缓存；历史 asOf 条目仅当被新事件序号覆盖时失效 */
  private invalidateRange(from: ISODate, to: ISODate, eventSeq: number): void {
    for (const key of [...this.dayCache.keys()]) {
      const hash = key.indexOf('#');
      const date = key.slice(0, hash);
      const tag = key.slice(hash + 1);
      if (date < from || date >= to) continue;
      if (tag === 'L' || Number(tag) >= eventSeq) this.dayCache.delete(key);
    }
  }
}

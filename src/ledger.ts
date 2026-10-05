import { Frac, type FracInput } from "./fraction.ts";
import { listDays, toDateString, toDayIndex } from "./dates.ts";

export interface RevisionInput {
  seq: number; // 录入序号，全局唯一
  ruleId: string;
  from: string; // 业务有效区间 [from, to)，半开
  to: string;
  weights: Record<string, FracInput>; // 一整套指标权重
}

export interface ScoreEvent {
  date: string;
  metric: string;
  score: FracInput;
}

interface RevisionEntry {
  kind: "revision";
  seq: number;
  ruleId: string;
  from: string;
  to: string;
  weights: Record<string, Frac>;
  retractedBy: number | null; // 撤销它的录入序号
}

interface RetractionEntry {
  kind: "retraction";
  seq: number;
  targetSeq: number;
}

type Entry = RevisionEntry | RetractionEntry;

export interface DayResult {
  date: string;
  status: "ok" | "missing";
  ruleId: string | null;
  ruleSeq: number | null;
  score: Frac | null; // 当日加权分
  missingMetrics: string[]; // 缺分的指标
  reason: "no-rule" | "missing-scores" | null;
}

export interface Segment {
  from: string;
  to: string;
  ruleId: string | null; // 该日期段采用的规则版本
  ruleSeq: number | null;
}

export interface QuarterReport {
  from: string;
  to: string;
  asOfSeq: number | null; // null 表示当前最新视角
  days: DayResult[];
  segments: Segment[]; // 受修订影响、按规则版本切分的日期段
  total: Frac | null; // 完整天的算术平均；缺分天不计入也不当 0
  completeDays: number;
  missingDays: { date: string; reason: string; missingMetrics: string[] }[];
}

export class Ledger {
  private entries = new Map<number, Entry>();
  private scores = new Map<string, Map<string, Frac>>();
  // 仅缓存“当前视角”的逐日结果；历史 asOf 查询不写入，避免污染。
  private cache = new Map<string, DayResult>();

  // ---------- 录入 ----------

  addRevision(input: RevisionInput): void {
    this.claimSeq(input.seq);
    if (toDayIndex(input.from) >= toDayIndex(input.to)) {
      throw new Error(`业务有效区间为空: [${input.from}, ${input.to})`);
    }
    const metrics = Object.keys(input.weights);
    if (metrics.length === 0) throw new Error("权重表不能为空");
    const weights: Record<string, Frac> = {};
    let sum = new Frac(0n);
    for (const metric of metrics) {
      const w = Frac.of(input.weights[metric]);
      if (w.isNegative()) throw new Error(`权重必须非负: ${metric} = ${w}`);
      weights[metric] = w;
      sum = sum.add(w);
    }
    if (!sum.equals(new Frac(1n))) {
      throw new Error(`权重和必须为 1，实际为 ${sum}`);
    }
    this.entries.set(input.seq, {
      kind: "revision",
      seq: input.seq,
      ruleId: input.ruleId,
      from: input.from,
      to: input.to,
      weights,
      retractedBy: null,
    });
    this.invalidateRange(input.from, input.to);
  }

  addRevisions(inputs: RevisionInput[]): void {
    for (const input of inputs) this.addRevision(input);
  }

  /** 撤销引用某修订；撤销本身也是一条录入，只在撤销序号之后（含）的查询视角生效。 */
  retract(seq: number, targetSeq: number): void {
    this.claimSeq(seq);
    const target = this.entries.get(targetSeq);
    if (!target || target.kind !== "revision") {
      throw new Error(`撤销目标不存在: 录入序号 ${targetSeq}`);
    }
    if (target.retractedBy !== null) {
      throw new Error(`录入序号 ${targetSeq} 已被撤销（撤销序号 ${target.retractedBy}），不能重复撤销`);
    }
    target.retractedBy = seq;
    this.entries.set(seq, { kind: "retraction", seq, targetSeq });
    this.invalidateRange(target.from, target.to);
  }

  addScore(date: string, metric: string, score: FracInput): void {
    toDayIndex(date);
    let day = this.scores.get(date);
    if (!day) {
      day = new Map();
      this.scores.set(date, day);
    }
    day.set(metric, Frac.of(score)); // 同日同指标后录入的事件覆盖先录入的
    this.cache.delete(date);
  }

  /** 批量补录得分事件。 */
  addScores(events: ScoreEvent[]): void {
    for (const e of events) this.addScore(e.date, e.metric, e.score);
  }

  // ---------- 查询 ----------

  /** 当天可见（序号 <= asOfSeq 且未被可见撤销命中）的最新一整套权重。 */
  resolveRule(date: string, asOfSeq?: number): RevisionEntry | null {
    const limit = asOfSeq ?? Number.POSITIVE_INFINITY;
    let best: RevisionEntry | null = null;
    for (const entry of this.entries.values()) {
      if (entry.kind !== "revision") continue;
      if (entry.seq > limit) continue;
      if (entry.retractedBy !== null && entry.retractedBy <= limit) continue;
      if (!(entry.from <= date && date < entry.to)) continue;
      if (!best || entry.seq > best.seq) best = entry;
    }
    return best;
  }

  /** 纯计算单日结果，不读也不写缓存。 */
  computeDay(date: string, asOfSeq?: number): DayResult {
    const rev = this.resolveRule(date, asOfSeq);
    if (!rev) {
      return {
        date,
        status: "missing",
        ruleId: null,
        ruleSeq: null,
        score: null,
        missingMetrics: [],
        reason: "no-rule",
      };
    }
    const dayScores = this.scores.get(date);
    const missing: string[] = [];
    let acc = new Frac(0n);
    for (const metric of Object.keys(rev.weights)) {
      const s = dayScores?.get(metric);
      if (s === undefined) {
        missing.push(metric);
      } else {
        acc = acc.add(rev.weights[metric].mul(s));
      }
    }
    if (missing.length > 0) {
      return {
        date,
        status: "missing",
        ruleId: rev.ruleId,
        ruleSeq: rev.seq,
        score: null,
        missingMetrics: missing.sort(),
        reason: "missing-scores",
      };
    }
    return {
      date,
      status: "ok",
      ruleId: rev.ruleId,
      ruleSeq: rev.seq,
      score: acc,
      missingMetrics: [],
      reason: null,
    };
  }

  /**
   * 季度报表。asOfSeq 省略时为当前视角（走逐日缓存，增量计算）；
   * 传入 asOfSeq 时为历史视角（纯计算，不触碰缓存）。
   */
  quarterReport(from: string, to: string, asOfSeq?: number): QuarterReport {
    if (asOfSeq === undefined) {
      return this.buildReport(from, to, null, (date) => {
        const hit = this.cache.get(date);
        if (hit) return hit;
        const fresh = this.computeDay(date);
        this.cache.set(date, fresh);
        return fresh;
      });
    }
    return this.buildReport(from, to, asOfSeq, (date) => this.computeDay(date, asOfSeq));
  }

  /** 独立逐日扫描：完全绕过缓存，用于对照增量结果。 */
  scanQuarter(from: string, to: string, asOfSeq?: number): QuarterReport {
    const limit = asOfSeq ?? null;
    return this.buildReport(from, to, limit, (date) => this.computeDay(date, asOfSeq));
  }

  /** 双时点查询：then = 当时看到的，now = 按现在规则重算的。 */
  compareQuarter(
    from: string,
    to: string,
    thenSeq: number,
  ): { then: QuarterReport; now: QuarterReport } {
    return {
      then: this.quarterReport(from, to, thenSeq),
      now: this.quarterReport(from, to),
    };
  }

  /** 当前缓存覆盖的日期（用于观察失效范围）。 */
  cachedDates(): string[] {
    return [...this.cache.keys()].sort();
  }

  // ---------- 内部 ----------

  private claimSeq(seq: number): void {
    if (!Number.isInteger(seq) || seq <= 0) {
      throw new Error(`录入序号必须为正整数: ${seq}`);
    }
    if (this.entries.has(seq)) {
      throw new Error(`同序号冲突: 录入序号 ${seq} 已存在`);
    }
  }

  private invalidateRange(from: string, to: string): void {
    for (const key of [...this.cache.keys()]) {
      if (key >= from && key < to) this.cache.delete(key);
    }
  }

  private buildReport(
    from: string,
    to: string,
    asOfSeq: number | null,
    getDay: (date: string) => DayResult,
  ): QuarterReport {
    const dates = listDays(from, to);
    const days = dates.map(getDay);

    const segments: Segment[] = [];
    for (const day of days) {
      const last = segments[segments.length - 1];
      if (last && last.ruleSeq === day.ruleSeq && last.ruleId === day.ruleId) {
        last.to = toDateString(toDayIndex(day.date) + 1);
      } else {
        segments.push({
          from: day.date,
          to: toDateString(toDayIndex(day.date) + 1),
          ruleId: day.ruleId,
          ruleSeq: day.ruleSeq,
        });
      }
    }

    let sum = new Frac(0n);
    let completeDays = 0;
    const missingDays: QuarterReport["missingDays"] = [];
    for (const day of days) {
      if (day.status === "ok" && day.score) {
        sum = sum.add(day.score);
        completeDays++;
      } else {
        missingDays.push({
          date: day.date,
          reason: day.reason ?? "missing-scores",
          missingMetrics: day.missingMetrics,
        });
      }
    }

    return {
      from,
      to,
      asOfSeq,
      days,
      segments,
      total: completeDays > 0 ? sum.div(new Frac(BigInt(completeDays))) : null,
      completeDays,
      missingDays,
    };
  }
}

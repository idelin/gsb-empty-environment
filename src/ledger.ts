import { signPayload } from './signature.ts';
import type {
  Day,
  KpiId,
  KpiContribution,
  KpiParamEntry,
  ScoreReport,
  SegmentScore,
  Version,
  VersionImpact,
  VersionInput,
} from './types.ts';

const EPSILON = 1e-6;

export interface LedgerOptions {
  periodFrom: Day; // 考核周期起始日（含）
  periodTo: Day;   // 考核周期结束日（不含）
  keys: Record<string, string>; // 责任人 -> 签名密钥
}

interface SignedPayload {
  id: number;
  author: string;
  recordedAtDay: Day;
  reason: string;
  entries: KpiParamEntry[];
}

function sortEntries(entries: KpiParamEntry[]): KpiParamEntry[] {
  return [...entries].sort(
    (a, b) =>
      a.kpiId.localeCompare(b.kpiId) ||
      a.validFrom - b.validFrom ||
      a.validTo - b.validTo,
  );
}

function signedPayloadOf(version: SignedPayload): SignedPayload {
  return {
    id: version.id,
    author: version.author,
    recordedAtDay: version.recordedAtDay,
    reason: version.reason,
    entries: sortEntries(version.entries),
  };
}

/**
 * 员工绩效考评时态指标账本。
 *
 * - 每次 KPI 目标/权重调整提交为一个带责任人签名的不可变版本；
 * - 版本内参数按 [validFrom, validTo) 生效，允许追溯（recordedAtDay 晚于生效日）；
 * - 季度末得分按参数恒定分段、以天数加权汇总；
 * - 支持按历史版本快照重算（考核推演），并记录每次版本提交的得分影响。
 */
export class TemporalKpiLedger {
  private readonly periodFrom: Day;
  private readonly periodTo: Day;
  private readonly keys: Record<string, string>;
  private readonly versions: Version[] = [];
  private readonly actuals = new Map<KpiId, number>();
  private readonly impactLog: VersionImpact[] = [];

  constructor(options: LedgerOptions) {
    if (!(options.periodFrom < options.periodTo)) {
      throw new Error('考核周期非法：periodFrom 必须小于 periodTo');
    }
    this.periodFrom = options.periodFrom;
    this.periodTo = options.periodTo;
    this.keys = { ...options.keys };
  }

  /** 录入 KPI 实际完成值 */
  recordActual(kpiId: KpiId, actual: number): void {
    if (!Number.isFinite(actual) || actual < 0) {
      throw new Error(`KPI ${kpiId} 实际值非法: ${actual}`);
    }
    this.actuals.set(kpiId, actual);
  }

  /** 提交新版本（含全周期覆盖校验与责任人签名），并记录版本影响 */
  commitVersion(input: VersionInput): Version {
    const secret = this.keys[input.author];
    if (secret === undefined) {
      throw new Error(`未知责任人，无法签名: ${input.author}`);
    }
    const entries = sortEntries(input.entries).map((entry) => ({ ...entry }));
    this.validateEntries(entries);

    const id = this.versions.length + 1;
    const payload: SignedPayload = {
      id,
      author: input.author,
      recordedAtDay: input.recordedAtDay,
      reason: input.reason,
      entries,
    };
    const version: Version = { ...payload, signature: signPayload(secret, signedPayloadOf(payload)) };
    this.versions.push(version);
    this.recordImpact(version);
    return version;
  }

  listVersions(): Version[] {
    return this.versions.map((version) => ({ ...version, entries: version.entries.map((entry) => ({ ...entry })) }));
  }

  getVersion(id: number): Version {
    const version = this.versions.find((item) => item.id === id);
    if (!version) throw new Error(`版本不存在: ${id}`);
    return version;
  }

  /** 校验版本签名（防篡改） */
  verifyVersion(id: number): boolean {
    const version = this.getVersion(id);
    const secret = this.keys[version.author];
    if (secret === undefined) return false;
    return signPayload(secret, signedPayloadOf(version)) === version.signature;
  }

  /** 历史战略快照推演：按指定版本重算季度末最终加权得分；缺省取最新版本 */
  computeScore(asOfVersionId?: number): ScoreReport {
    const version = asOfVersionId === undefined ? this.versions[this.versions.length - 1] : this.getVersion(asOfVersionId);
    if (!version) throw new Error('账本为空，尚无版本');
    return this.scoreOf(version);
  }

  /** 调整前后对比：两个版本快照下的得分差 */
  compareVersions(baseVersionId: number, targetVersionId: number): { base: ScoreReport; target: ScoreReport; delta: number } {
    const base = this.computeScore(baseVersionId);
    const target = this.computeScore(targetVersionId);
    return { base, target, delta: target.totalScore - base.totalScore };
  }

  /** 版本影响记录 */
  impacts(): VersionImpact[] {
    return this.impactLog.map((impact) => ({ ...impact }));
  }

  private validateEntries(entries: KpiParamEntry[]): void {
    if (entries.length === 0) throw new Error('版本必须包含至少一条 KPI 参数');
    for (const entry of entries) {
      if (!(entry.validFrom < entry.validTo)) {
        throw new Error(`KPI ${entry.kpiId} 有效区间非法: [${entry.validFrom}, ${entry.validTo})`);
      }
      if (entry.validFrom < this.periodFrom || entry.validTo > this.periodTo) {
        throw new Error(`KPI ${entry.kpiId} 有效区间越出考核周期 [${this.periodFrom}, ${this.periodTo})`);
      }
      if (!(entry.weight >= 0 && entry.weight <= 1)) {
        throw new Error(`KPI ${entry.kpiId} 权重非法: ${entry.weight}`);
      }
      if (!(entry.target > 0)) {
        throw new Error(`KPI ${entry.kpiId} 目标值必须为正: ${entry.target}`);
      }
    }
    // 全周期每日权重完备性：每一天权重之和必须为 1，且同一 KPI 区间不得重叠
    for (const [from, to] of this.segmentsOf(entries)) {
      const active = this.activeEntries(entries, from);
      const seen = new Set<KpiId>();
      let sum = 0;
      for (const entry of active) {
        if (seen.has(entry.kpiId)) {
          throw new Error(`KPI ${entry.kpiId} 在 [${from}, ${to}) 存在重叠区间`);
        }
        seen.add(entry.kpiId);
        sum += entry.weight;
      }
      if (Math.abs(sum - 1) > EPSILON) {
        throw new Error(`第 ${from} 天起权重之和为 ${sum}，须等于 1（全周期覆盖校验失败）`);
      }
    }
  }

  private segmentsOf(entries: KpiParamEntry[]): Array<[Day, Day]> {
    const boundaries = new Set<Day>([this.periodFrom, this.periodTo]);
    for (const entry of entries) {
      boundaries.add(entry.validFrom);
      boundaries.add(entry.validTo);
    }
    const sorted = [...boundaries].sort((a, b) => a - b);
    const segments: Array<[Day, Day]> = [];
    for (let index = 0; index + 1 < sorted.length; index += 1) {
      segments.push([sorted[index], sorted[index + 1]]);
    }
    return segments;
  }

  private activeEntries(entries: KpiParamEntry[], day: Day): KpiParamEntry[] {
    return entries.filter((entry) => entry.validFrom <= day && day < entry.validTo);
  }

  private scoreOf(version: Version): ScoreReport {
    const segments: SegmentScore[] = [];
    let totalScore = 0;
    const totalDays = this.periodTo - this.periodFrom;
    for (const [from, to] of this.segmentsOf(version.entries)) {
      const days = to - from;
      const contributions: KpiContribution[] = this.activeEntries(version.entries, from).map((entry) => {
        const actual = this.actuals.get(entry.kpiId);
        if (actual === undefined) {
          throw new Error(`缺少 KPI ${entry.kpiId} 的实际完成值，无法计分`);
        }
        const rate = actual / entry.target;
        return {
          kpiId: entry.kpiId,
          weight: entry.weight,
          target: entry.target,
          actual,
          rate,
          weighted: entry.weight * rate,
        };
      });
      const score = contributions.reduce((acc, item) => acc + item.weighted, 0);
      segments.push({ from, to, days, score, contributions });
      totalScore += (score * days) / totalDays;
    }
    return {
      versionId: version.id,
      periodFrom: this.periodFrom,
      periodTo: this.periodTo,
      totalScore,
      segments,
    };
  }

  private recordImpact(version: Version): void {
    const previous = this.versions[this.versions.length - 2] ?? null;
    let previousScore: number | null = null;
    let newScore: number | null = null;
    try {
      newScore = this.scoreOf(version).totalScore;
      if (previous) previousScore = this.scoreOf(previous).totalScore;
    } catch {
      // 实际完成值尚未录入时，影响待计分数据齐备后通过 compareVersions 推演
    }
    this.impactLog.push({
      versionId: version.id,
      author: version.author,
      recordedAtDay: version.recordedAtDay,
      reason: version.reason,
      previousVersionId: previous ? previous.id : null,
      previousScore,
      newScore,
      delta: previousScore !== null && newScore !== null ? newScore - previousScore : null,
    });
  }
}

import { createHmac } from "node:crypto";

export interface KpiVersionInput {
  kpiId: string;
  targetValue: number;
  weight: number;
  validFrom: number;
  validTo: number;
  actor: string;
  reason: string;
  recordedAtDay: number;
}

export interface KpiVersion extends KpiVersionInput {
  seq: number;
  signature: string;
}

export interface ActualRecord {
  kpiId: string;
  day: number;
  value: number;
}

export interface SegmentContribution {
  kpiId: string;
  validFrom: number;
  validTo: number;
  weight: number;
  targetValue: number;
  avgActual: number | null;
  attainment: number | null;
  segmentFraction: number;
  points: number;
  sourceSeq: number;
}

export interface ScoreReport {
  asOfSeq: number;
  totalScore: number;
  contributions: SegmentContribution[];
  warnings: string[];
}

export interface KpiScoreDelta {
  kpiId: string;
  before: number;
  after: number;
  delta: number;
}

export interface ScoreDiff {
  fromSeq: number;
  toSeq: number;
  beforeScore: number;
  afterScore: number;
  delta: number;
  perKpi: KpiScoreDelta[];
  impactingVersions: KpiVersion[];
}

export interface LedgerOptions {
  cycleStartDay: number;
  cycleEndDay: number;
  signingSecret?: string;
}

const EPSILON = 1e-9;

export class TemporalKpiLedger {
  readonly #versions: KpiVersion[] = [];
  readonly #actuals: ActualRecord[] = [];
  readonly #cycleStart: number;
  readonly #cycleEnd: number;
  readonly #secret: string;

  constructor(options: LedgerOptions) {
    if (!(options.cycleStartDay < options.cycleEndDay)) {
      throw new Error("cycleStartDay 必须小于 cycleEndDay");
    }
    this.#cycleStart = options.cycleStartDay;
    this.#cycleEnd = options.cycleEndDay;
    this.#secret = options.signingSecret ?? "temporal-kpi-ledger";
  }

  get cycleStartDay(): number {
    return this.#cycleStart;
  }

  get cycleEndDay(): number {
    return this.#cycleEnd;
  }

  recordVersion(input: KpiVersionInput): KpiVersion {
    if (!input.kpiId) throw new Error("kpiId 不能为空");
    if (!input.actor) throw new Error("actor（变更责任人）不能为空");
    if (!(input.weight >= 0 && input.weight <= 1)) {
      throw new Error(`权重必须在 [0, 1] 区间，收到 ${input.weight}`);
    }
    if (!(input.targetValue > 0)) {
      throw new Error(`目标值必须为正数，收到 ${input.targetValue}`);
    }
    if (!(input.validFrom < input.validTo)) {
      throw new Error(`有效区间非法：[${input.validFrom}, ${input.validTo})`);
    }
    if (input.validFrom < this.#cycleStart || input.validTo > this.#cycleEnd) {
      throw new Error(
        `有效区间 [${input.validFrom}, ${input.validTo}) 超出考评周期 [${this.#cycleStart}, ${this.#cycleEnd})`,
      );
    }
    const version: KpiVersion = { ...input, seq: this.#versions.length + 1, signature: "" };
    version.signature = this.#sign(version);
    this.#versions.push(version);
    return version;
  }

  recordActual(kpiId: string, day: number, value: number): ActualRecord {
    if (day < this.#cycleStart || day >= this.#cycleEnd) {
      throw new Error(`实际值日期 ${day} 超出考评周期 [${this.#cycleStart}, ${this.#cycleEnd})`);
    }
    const record: ActualRecord = { kpiId, day, value };
    this.#actuals.push(record);
    return record;
  }

  listVersions(asOfSeq: number = Number.POSITIVE_INFINITY): KpiVersion[] {
    return this.#versions.filter((v) => v.seq <= asOfSeq).map((v) => ({ ...v }));
  }

  verifyVersion(version: KpiVersion): boolean {
    return this.#sign(version) === version.signature;
  }

  computeScore(asOfSeq: number = Number.POSITIVE_INFINITY): ScoreReport {
    const versions = this.#versions.filter((v) => v.seq <= asOfSeq);
    const boundaries = new Set<number>([this.#cycleStart, this.#cycleEnd]);
    for (const v of versions) {
      boundaries.add(Math.max(v.validFrom, this.#cycleStart));
      boundaries.add(Math.min(v.validTo, this.#cycleEnd));
    }
    const sorted = [...boundaries].sort((a, b) => a - b);
    const cycleDays = this.#cycleEnd - this.#cycleStart;
    const contributions: SegmentContribution[] = [];
    const warnings: string[] = [];
    const kpiIds = [...new Set(versions.map((v) => v.kpiId))].sort();

    for (let i = 0; i < sorted.length - 1; i++) {
      const segFrom = sorted[i];
      const segTo = sorted[i + 1];
      const mid = (segFrom + segTo) / 2;
      const fraction = (segTo - segFrom) / cycleDays;
      let weightSum = 0;
      let covered = false;

      for (const kpiId of kpiIds) {
        const covering = versions
          .filter((v) => v.kpiId === kpiId && v.validFrom <= mid && mid < v.validTo)
          .sort((a, b) => b.seq - a.seq)[0];
        if (!covering) continue;
        covered = true;
        weightSum += covering.weight;

        const segActuals = this.#actuals.filter(
          (a) => a.kpiId === kpiId && a.day >= segFrom && a.day < segTo,
        );
        const avgActual =
          segActuals.length === 0
            ? null
            : segActuals.reduce((sum, a) => sum + a.value, 0) / segActuals.length;
        const attainment = avgActual === null ? null : avgActual / covering.targetValue;
        const points = attainment === null ? 0 : covering.weight * attainment * fraction * 100;
        if (attainment === null) {
          warnings.push(`指标 ${kpiId} 在区间 [${segFrom}, ${segTo}) 缺少实际值，按 0 分计入`);
        }
        contributions.push({
          kpiId,
          validFrom: segFrom,
          validTo: segTo,
          weight: covering.weight,
          targetValue: covering.targetValue,
          avgActual,
          attainment,
          segmentFraction: fraction,
          points,
          sourceSeq: covering.seq,
        });
      }

      if (covered && Math.abs(weightSum - 1) > EPSILON) {
        warnings.push(`区间 [${segFrom}, ${segTo}) 权重合计为 ${weightSum}，不等于 1`);
      }
      if (!covered) {
        const orphanCount = this.#actuals.filter(
          (a) => a.day >= segFrom && a.day < segTo,
        ).length;
        if (orphanCount > 0) {
          warnings.push(`区间 [${segFrom}, ${segTo}) 无生效版本，${orphanCount} 条实际值未计入`);
        }
      }
    }

    const totalScore = contributions.reduce((sum, c) => sum + c.points, 0);
    return {
      asOfSeq: asOfSeq === Number.POSITIVE_INFINITY ? this.#versions.length : asOfSeq,
      totalScore,
      contributions,
      warnings,
    };
  }

  diffScores(fromSeq: number, toSeq: number): ScoreDiff {
    if (!(fromSeq < toSeq)) throw new Error("fromSeq 必须小于 toSeq");
    const before = this.computeScore(fromSeq);
    const after = this.computeScore(toSeq);

    const perKpiMap = new Map<string, KpiScoreDelta>();
    const accumulate = (report: ScoreReport, key: "before" | "after") => {
      for (const c of report.contributions) {
        const entry = perKpiMap.get(c.kpiId) ?? { kpiId: c.kpiId, before: 0, after: 0, delta: 0 };
        entry[key] += c.points;
        perKpiMap.set(c.kpiId, entry);
      }
    };
    accumulate(before, "before");
    accumulate(after, "after");
    const perKpi = [...perKpiMap.values()]
      .map((e) => ({ ...e, delta: e.after - e.before }))
      .sort((a, b) => a.kpiId.localeCompare(b.kpiId));

    return {
      fromSeq,
      toSeq,
      beforeScore: before.totalScore,
      afterScore: after.totalScore,
      delta: after.totalScore - before.totalScore,
      perKpi,
      impactingVersions: this.#versions.filter((v) => v.seq > fromSeq && v.seq <= toSeq),
    };
  }

  #sign(version: KpiVersion): string {
    const payload = JSON.stringify({
      seq: version.seq,
      kpiId: version.kpiId,
      targetValue: version.targetValue,
      weight: version.weight,
      validFrom: version.validFrom,
      validTo: version.validTo,
      actor: version.actor,
      reason: version.reason,
      recordedAtDay: version.recordedAtDay,
    });
    return createHmac("sha256", this.#secret).update(payload).digest("hex");
  }
}

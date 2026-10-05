// 流式有限模式匹配器：模式为「A 之后在窗口 W 内出现 B，且 [A.time, B.time] 内没有 C」。
//
// 语义要点：
// - 窗口两端均包含；C 与端点同时间同样使匹配无效（C.time ∈ [A.time, B.time] 即否决）。
// - 日志按到达顺序消费；告警按事件时间计算；同时间事件按事件 ID 决定确定序。
// - 修订：更高 rev 可改类型/时间/撤销；同 rev 同内容幂等；同 rev 不同内容为冲突并拒绝。
// - 水位线单调；rec.time < watermark - allowedLateness 的修订被拒绝且不改变状态。
// - 修订目标事件的当前时间超出允许迟到范围（existing.time < watermark - L）时同样
//   拒绝：其旧位置的影响面 [t-W, t] 已部分封闭，接受它会改变已归档告警。
//   因此封闭线 horizon = watermark - L - W 之前的匹配状态可以安全裁剪（封闭即最终），
//   且流式与一次性参考结果一致。

import { alertIdOf } from './types.ts';
import type {
  Change,
  InputRecord,
  Kind,
  RetractRecord,
  UpsertRecord,
} from './types.ts';
import { TimeIndex } from './time-index.ts';

interface EventState {
  id: string;
  rev: number;
  kind: Kind | null; // null = tombstone（已撤销）
  time: number;
}

export interface EngineStats {
  accepted: number;
  rejectedLate: number;
  staleRevisions: number;
  idempotentDuplicates: number;
  conflicts: number;
}

export interface AlertInfo {
  aId: string;
  bId: string;
}

export class PatternEngine {
  readonly windowSize: number;
  readonly allowedLateness: number;

  // 修订注册表：每个出现过的 ID 一条 O(1) 记录（含 tombstone），不随水位线裁剪，
  // 用于修订单调性 / 幂等 / 冲突 / 封闭区拒绝判定；生产环境可外置为 KV 存储。
  private byId = new Map<string, EventState>();

  // 匹配热状态：仅保留 time >= horizon 的事件，随开放窗口有界。
  private indexA = new TimeIndex();
  private indexB = new TimeIndex();
  private indexC = new TimeIndex();
  private openAlerts = new Map<string, AlertInfo>();
  private alertsByA = new Map<string, Set<string>>();

  // 已封闭（归档）告警：最终有效，不再变化；生产环境可外置存储。
  private archivedAlerts = new Map<string, AlertInfo>();

  private watermark = Number.NEGATIVE_INFINITY;
  readonly stats: EngineStats = {
    accepted: 0,
    rejectedLate: 0,
    staleRevisions: 0,
    idempotentDuplicates: 0,
    conflicts: 0,
  };

  constructor(windowSize: number, allowedLateness: number) {
    this.windowSize = windowSize;
    this.allowedLateness = allowedLateness;
  }

  get currentWatermark(): number {
    return this.watermark;
  }

  private latenessLine(): number {
    return this.watermark - this.allowedLateness;
  }

  private closedHorizon(): number {
    return this.watermark - this.allowedLateness - this.windowSize;
  }

  /** 应用一条输入记录，返回产生的变更（upsert/retract）。 */
  apply(record: InputRecord): Change[] {
    switch (record.type) {
      case 'watermark':
        return this.advanceWatermark(record.time);
      case 'upsert':
        return this.applyUpsert(record);
      case 'retract':
        return this.applyRetract(record);
    }
  }

  applyAll(records: Iterable<InputRecord>): Change[] {
    const out: Change[] = [];
    for (const r of records) out.push(...this.apply(r));
    return out;
  }

  private applyUpsert(rec: UpsertRecord): Change[] {
    if (rec.time < this.latenessLine()) {
      this.stats.rejectedLate++;
      return [];
    }
    const existing = this.byId.get(rec.id);
    if (existing && existing.time < this.latenessLine()) {
      this.stats.rejectedLate++; // 目标事件已超出允许迟到范围：拒绝，封闭即最终
      return [];
    }
    if (existing) {
      if (rec.rev < existing.rev) {
        this.stats.staleRevisions++;
        return [];
      }
      if (rec.rev === existing.rev) {
        if (existing.kind === rec.kind && existing.time === rec.time) {
          this.stats.idempotentDuplicates++;
          return []; // 同 rev 同内容：幂等
        }
        this.stats.conflicts++;
        return []; // 同 rev 不同内容：冲突，拒绝且不改变状态
      }
    }
    this.stats.accepted++;
    const changes: Change[] = [];
    if (existing && existing.kind !== null) {
      this.removeEvent(existing, changes);
    }
    const state: EventState = { id: rec.id, rev: rec.rev, kind: rec.kind, time: rec.time };
    this.byId.set(rec.id, state);
    this.addEvent(state, changes);
    return changes;
  }

  private applyRetract(rec: RetractRecord): Change[] {
    if (rec.time < this.latenessLine()) {
      this.stats.rejectedLate++;
      return [];
    }
    const existing = this.byId.get(rec.id);
    if (existing && existing.time < this.latenessLine()) {
      this.stats.rejectedLate++;
      return [];
    }
    if (existing) {
      if (rec.rev < existing.rev) {
        this.stats.staleRevisions++;
        return [];
      }
      if (rec.rev === existing.rev) {
        if (existing.kind === null) {
          this.stats.idempotentDuplicates++;
          return []; // 重复撤销：幂等
        }
        this.stats.conflicts++;
        return []; // 同 rev 的 upsert 与 retract 冲突
      }
    }
    this.stats.accepted++;
    const changes: Change[] = [];
    if (existing && existing.kind !== null) {
      this.removeEvent(existing, changes);
    }
    this.byId.set(rec.id, { id: rec.id, rev: rec.rev, kind: null, time: rec.time });
    return changes;
  }

  private indexOf(kind: Kind): TimeIndex {
    return kind === 'A' ? this.indexA : kind === 'B' ? this.indexB : this.indexC;
  }

  private addEvent(state: EventState, changes: Change[]): void {
    const kind = state.kind!;
    this.indexOf(kind).insert(state.time, state.id);
    if (kind === 'A') {
      this.recomputeA(state.id, changes);
    } else {
      this.recomputeAffectedAs(state.time, changes);
    }
  }

  private removeEvent(state: EventState, changes: Change[]): void {
    const kind = state.kind!;
    this.indexOf(kind).remove(state.time, state.id);
    if (kind === 'A') {
      const ids = this.alertsByA.get(state.id);
      if (ids) {
        for (const alertId of [...ids].sort()) {
          this.openAlerts.delete(alertId);
          changes.push({ type: 'retract', alertId });
        }
        this.alertsByA.delete(state.id);
      }
    } else {
      this.recomputeAffectedAs(state.time, changes);
    }
  }

  /** B 或 C 在时间 t 变化：影响 A.time ∈ [t - W, t] 的所有 A。 */
  private recomputeAffectedAs(t: number, changes: Change[]): void {
    const aIds = this.indexA.rangeClosed(t - this.windowSize, t);
    for (const aId of aIds) this.recomputeA(aId, changes);
  }

  /** 重算单个 A 的匹配集合，并与现状 diff 产生 upsert/retract。 */
  private recomputeA(aId: string, changes: Change[]): void {
    const a = this.byId.get(aId);
    if (!a || a.kind !== 'A') return;
    const aTime = a.time;
    const upper = aTime + this.windowSize;
    const c0 = this.indexC.firstTimeAtOrAfter(aTime);
    // 匹配条件：B.time ∈ [aTime, upper] 且不存在 C.time ∈ [aTime, B.time]。
    // 若首个 C 落在窗口内，则 B.time 必须严格小于 c0（C 与 B 同时间也否决）。
    const bIds =
      c0 !== undefined && c0 <= upper
        ? this.indexB.rangeClosedOpen(aTime, c0)
        : this.indexB.rangeClosed(aTime, upper);

    const desired = new Set(bIds.map((bId) => alertIdOf(aId, bId)));
    const current = this.alertsByA.get(aId) ?? new Set<string>();

    for (const alertId of [...current].sort()) {
      if (!desired.has(alertId)) {
        this.openAlerts.delete(alertId);
        current.delete(alertId);
        changes.push({ type: 'retract', alertId });
      }
    }
    for (const bId of bIds) {
      const alertId = alertIdOf(aId, bId);
      if (!current.has(alertId)) {
        current.add(alertId);
        this.openAlerts.set(alertId, { aId, bId });
        changes.push({ type: 'upsert', alertId, aId, bId });
      }
    }
    if (current.size > 0) this.alertsByA.set(aId, current);
    else this.alertsByA.delete(aId);
  }

  private advanceWatermark(wm: number): Change[] {
    if (wm <= this.watermark) return []; // 水位线单调
    this.watermark = wm;
    const horizon = this.closedHorizon();
    // A.time < horizon 的告警封闭：归档（仍在有效集合中），A 移出热索引。
    for (const { id } of this.indexA.drainBefore(horizon)) {
      const ids = this.alertsByA.get(id);
      if (ids) {
        for (const alertId of ids) {
          const info = this.openAlerts.get(alertId)!;
          this.openAlerts.delete(alertId);
          this.archivedAlerts.set(alertId, info);
        }
        this.alertsByA.delete(id);
      }
    }
    // B/C 在封闭线之前的条目不再影响任何开放窗口，移出热索引。
    this.indexB.drainBefore(horizon);
    this.indexC.drainBefore(horizon);
    return [];
  }

  /** 当前有效告警集合（开放 + 已归档），key 为稳定告警 ID。 */
  effectiveAlerts(): Map<string, AlertInfo> {
    const out = new Map<string, AlertInfo>();
    for (const [k, v] of this.archivedAlerts) out.set(k, v);
    for (const [k, v] of this.openAlerts) out.set(k, v);
    return out;
  }

  get openAlertCount(): number {
    return this.openAlerts.size;
  }

  get archivedAlertCount(): number {
    return this.archivedAlerts.size;
  }

  /** 热状态（时间索引）中的事件数：随开放窗口有界。 */
  get hotEventCount(): number {
    return this.indexA.size + this.indexB.size + this.indexC.size;
  }

  /** 修订注册表大小：O(出现过的不同事件 ID 数)。 */
  get registrySize(): number {
    return this.byId.size;
  }

  // ---- checkpoint / recovery ----

  snapshot(): string {
    return JSON.stringify({
      windowSize: this.windowSize,
      allowedLateness: this.allowedLateness,
      watermark: this.watermark === Number.NEGATIVE_INFINITY ? '-Infinity' : this.watermark,
      byId: [...this.byId.values()],
      indexA: this.indexA.toJSON(),
      indexB: this.indexB.toJSON(),
      indexC: this.indexC.toJSON(),
      openAlerts: [...this.openAlerts],
      alertsByA: [...this.alertsByA].map(([k, v]) => [k, [...v]]),
      archivedAlerts: [...this.archivedAlerts],
      stats: this.stats,
    });
  }

  static restore(json: string): PatternEngine {
    const d = JSON.parse(json);
    const engine = new PatternEngine(d.windowSize, d.allowedLateness);
    engine.watermark = d.watermark === '-Infinity' ? Number.NEGATIVE_INFINITY : d.watermark;
    engine.byId = new Map(d.byId.map((s: EventState) => [s.id, s]));
    engine.indexA = TimeIndex.fromJSON(d.indexA);
    engine.indexB = TimeIndex.fromJSON(d.indexB);
    engine.indexC = TimeIndex.fromJSON(d.indexC);
    engine.openAlerts = new Map(d.openAlerts);
    engine.alertsByA = new Map(
      d.alertsByA.map(([k, v]: [string, string[]]) => [k, new Set(v)]),
    );
    engine.archivedAlerts = new Map(d.archivedAlerts);
    Object.assign(engine.stats, d.stats);
    return engine;
  }
}

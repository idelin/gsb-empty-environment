import { RationalFilter, cloneState } from './rationalFilter.ts';
import type { FilterState } from './rationalFilter.ts';
import type {
  ChangeCause,
  ChangeSet,
  GapFilterConfig,
  OutputChange,
  OutputOrigin,
  VersionEntry,
  VersionedOutput,
} from './types.ts';

interface StoredOutput {
  value: number;
  version: number;
  origin: OutputOrigin;
  /** 处理完该网格点后的滤波器状态快照，使局部重算可以从任意点恢复 */
  state: FilterState;
}

interface SummaryFacts {
  cause: ChangeCause;
  index: number;
  value: number;
  oldValue?: number;
  prev?: number;
  next?: number;
  oldGap?: number;
  oldGapWasBreak?: boolean;
  gapBefore?: number;
  gapAfter?: number;
  changes: OutputChange[];
}

function lowerBound(arr: number[], target: number): number {
  let lo = 0;
  let hi = arr.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (arr[mid] < target) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

/** 把有序索引列表压缩为 "3–5、7、9–10" 形式，便于阅读 */
function compressRanges(indices: number[]): string {
  if (indices.length === 0) return '';
  const parts: string[] = [];
  let start = indices[0];
  let prev = indices[0];
  for (let i = 1; i <= indices.length; i++) {
    if (i < indices.length && indices[i] === prev + 1) {
      prev = indices[i];
      continue;
    }
    parts.push(start === prev ? `${start}` : `${start}–${prev}`);
    if (i < indices.length) {
      start = indices[i];
      prev = indices[i];
    }
  }
  return parts.join('、');
}

/**
 * 缺口 tolerant 的有理滤波器。
 *
 * - 短缺口（缺失样本数 ≤ gapThreshold）：线性插值补齐，滤波状态连续；
 * - 长缺口（> gapThreshold）：断开滤波状态，后段从零状态重启；
 * - 迟到样本可能把长缺口拆成两个短缺口，从而桥接两个滤波段；
 *   此时只局部重算受影响的段尾（借助每个输出点上保存的状态快照），
 *   并以提高版本号的方式标记因补点而改变的历史结果。
 */
export class GapTolerantFilter {
  private readonly filter: RationalFilter;
  private readonly gapThreshold: number;

  /** 已到达的真实样本：有序索引 + 值 */
  private realIndices: number[] = [];
  private readonly realValues = new Map<number, number>();

  /** 网格点 → 最新输出（含插值点与状态快照） */
  private readonly outputs = new Map<number, StoredOutput>();
  /** 网格点 → 版本历史 */
  private readonly history = new Map<number, VersionEntry[]>();
  /** 每次 addSample 的变更集留档 */
  private readonly changeSets: ChangeSet[] = [];
  private currentVersion = 0;

  constructor(config: GapFilterConfig) {
    if (!Number.isInteger(config.gapThreshold) || config.gapThreshold < 0) {
      throw new Error('gapThreshold 必须是非负整数（缺失样本个数）');
    }
    this.filter = new RationalFilter(config);
    this.gapThreshold = config.gapThreshold;
  }

  get version(): number {
    return this.currentVersion;
  }

  /**
   * 写入一个样本（可乱序）。返回本次产生的版本化变更集，
   * 其中说明哪些历史结果因补点而改变。
   */
  addSample(index: number, value: number): ChangeSet {
    if (!Number.isInteger(index)) throw new Error('样本索引必须是整数');
    if (!Number.isFinite(value)) throw new Error('样本值必须是有限数');
    this.currentVersion += 1;
    if (this.realValues.has(index)) return this.correctSample(index, value);
    return this.insertSample(index, value);
  }

  // ---------------------------------------------------------------- 写入路径

  private insertSample(index: number, value: number): ChangeSet {
    const pos = lowerBound(this.realIndices, index);
    const prev = pos > 0 ? this.realIndices[pos - 1] : undefined;
    const next = pos < this.realIndices.length ? this.realIndices[pos] : undefined;

    const isFirst = this.realIndices.length === 0;
    const isAppend = !isFirst && next === undefined;

    const oldGap = prev !== undefined && next !== undefined ? next - prev - 1 : undefined;
    const oldGapWasBreak = oldGap !== undefined && oldGap > this.gapThreshold;
    const gapBefore = prev !== undefined ? index - prev - 1 : undefined;
    const gapAfter = next !== undefined ? next - index - 1 : undefined;
    const connectedBefore = gapBefore !== undefined && gapBefore <= this.gapThreshold;
    const connectedAfter = gapAfter !== undefined && gapAfter <= this.gapThreshold;

    this.realIndices.splice(pos, 0, index);
    this.realValues.set(index, value);

    // 局部重算起点：若与前段相连，从前一个真实样本的状态快照恢复；
    // 否则说明前面是长缺口（或没有前样本），从零状态新起一段。
    let startIndex: number;
    let state: FilterState;
    if (connectedBefore) {
      state = cloneState(this.outputs.get(prev as number)!.state);
      startIndex = (prev as number) + 1;
    } else {
      state = this.filter.zeroState();
      startIndex = index;
    }
    const endIndex = this.segmentEndFrom(index);

    const changes: OutputChange[] = [];
    this.recomputeRange(startIndex, endIndex, state, changes);

    const cause: ChangeCause = isFirst
      ? 'initial'
      : isAppend
        ? 'append'
        : oldGapWasBreak && connectedBefore && connectedAfter
          ? 'gap-bridged'
          : 'late-sample';

    return this.recordChangeSet(cause, index, value, startIndex, endIndex, changes, {
      prev,
      next,
      oldGap,
      oldGapWasBreak,
      gapBefore,
      gapAfter,
    });
  }

  private correctSample(index: number, value: number): ChangeSet {
    const oldValue = this.realValues.get(index)!;
    this.realValues.set(index, value);

    // 修正不改变分段，只需从该点（借前一点快照或零状态）重算到段尾。
    const before = this.outputs.get(index - 1);
    const state = before ? cloneState(before.state) : this.filter.zeroState();
    const endIndex = this.segmentEndFrom(index);

    const changes: OutputChange[] = [];
    this.recomputeRange(index, endIndex, state, changes);

    return this.recordChangeSet('correction', index, value, index, endIndex, changes, { oldValue });
  }

  // ---------------------------------------------------------------- 重算核心

  /** 从 index 出发沿短缺口走到所在段的最后一个真实样本索引 */
  private segmentEndFrom(index: number): number {
    let end = index;
    let cursor = index;
    for (;;) {
      const pos = lowerBound(this.realIndices, cursor + 1);
      if (pos >= this.realIndices.length) break;
      const n2 = this.realIndices[pos];
      if (n2 - cursor - 1 > this.gapThreshold) break;
      end = n2;
      cursor = n2;
    }
    return end;
  }

  /**
   * 用给定初始状态重算 [startIndex, endIndex] 的全部网格点。
   * 数值不变的点保留旧版本号（确定性重算，状态必然一致）。
   */
  private recomputeRange(
    startIndex: number,
    endIndex: number,
    state: FilterState,
    changes: OutputChange[],
  ): void {
    for (let g = startIndex; g <= endIndex; g++) {
      const real = this.realValues.get(g);
      const origin: OutputOrigin = real !== undefined ? 'real' : 'interpolated';
      const x = real !== undefined ? real : this.interpolateAt(g);
      const y = this.filter.step(state, x);
      const old = this.outputs.get(g);
      if (old === undefined) {
        changes.push({ index: g, kind: 'added', origin, newValue: y, newVersion: this.currentVersion });
        this.outputs.set(g, { value: y, version: this.currentVersion, origin, state: cloneState(state) });
        this.appendHistory(g, { version: this.currentVersion, value: y, origin });
      } else if (old.value !== y || old.origin !== origin) {
        changes.push({
          index: g,
          kind: 'updated',
          origin,
          oldValue: old.value,
          newValue: y,
          oldVersion: old.version,
          newVersion: this.currentVersion,
        });
        this.outputs.set(g, { value: y, version: this.currentVersion, origin, state: cloneState(state) });
        this.appendHistory(g, { version: this.currentVersion, value: y, origin });
      }
    }
  }

  /** g 处无真实样本，取两侧真实样本的线性插值（调用前保证两侧存在） */
  private interpolateAt(g: number): number {
    const pos = lowerBound(this.realIndices, g); // g 不是真实样本，pos 指向右侧样本
    const before = this.realIndices[pos - 1];
    const after = this.realIndices[pos];
    const x0 = this.realValues.get(before)!;
    const x1 = this.realValues.get(after)!;
    return x0 + ((x1 - x0) * (g - before)) / (after - before);
  }

  private appendHistory(index: number, entry: VersionEntry): void {
    let list = this.history.get(index);
    if (!list) {
      list = [];
      this.history.set(index, list);
    }
    list.push(entry);
  }

  // ---------------------------------------------------------------- 变更集与说明

  private recordChangeSet(
    cause: ChangeCause,
    index: number,
    value: number,
    from: number,
    to: number,
    changes: OutputChange[],
    extra: Partial<SummaryFacts>,
  ): ChangeSet {
    const changeSet: ChangeSet = {
      version: this.currentVersion,
      cause,
      sample: { index, value },
      affectedRange: { from, to },
      changes,
      summary: '',
    };
    changeSet.summary = this.buildSummary({
      cause,
      index,
      value,
      changes,
      ...extra,
    });
    this.changeSets.push(changeSet);
    return changeSet;
  }

  private buildSummary(f: SummaryFacts): string {
    const t = this.gapThreshold;
    const parts: string[] = [`版本 ${this.currentVersion}：`];
    switch (f.cause) {
      case 'initial':
        parts.push(`收到首个样本（索引 ${f.index}），以零初始状态开启滤波段。`);
        break;
      case 'append':
        if (f.gapBefore !== undefined && f.gapBefore > t) {
          parts.push(
            `索引 ${f.index} 追加样本；前方缺口 ${f.gapBefore} 个样本（> 阈值 ${t}），滤波状态断开，从零状态开启新段。`,
          );
        } else if (f.gapBefore !== undefined && f.gapBefore > 0) {
          parts.push(
            `索引 ${f.index} 追加样本；前方缺口 ${f.gapBefore} 个样本（≤ 阈值 ${t}），已线性插值补齐，滤波状态连续。`,
          );
        } else {
          parts.push(`索引 ${f.index} 追加样本，滤波状态连续。`);
        }
        break;
      case 'late-sample':
        parts.push(`索引 ${f.index} 补入迟到样本。`);
        if (f.oldGapWasBreak) {
          parts.push(
            `原长缺口（索引 ${f.prev}→${f.next}，缺 ${f.oldGap} 个样本）被缩短，但至少一侧仍超过阈值 ${t}，分段维持断开，仅做局部重算。`,
          );
        } else {
          parts.push(`所在短缺口（索引 ${f.prev}→${f.next}）的插值曲线改变，段内后续输出随之局部重算。`);
        }
        break;
      case 'gap-bridged':
        parts.push(
          `索引 ${f.index} 补入迟到样本，原长缺口（索引 ${f.prev}→${f.next}，缺 ${f.oldGap} 个样本，> 阈值 ${t}）` +
            `被拆成两个短缺口（分别缺 ${f.gapBefore}、${f.gapAfter} 个，均 ≤ 阈值），` +
            `两个滤波段桥接为一段，后段不再从零状态重启。`,
        );
        break;
      case 'correction':
        parts.push(`索引 ${f.index} 的样本值由 ${f.oldValue} 修正为 ${f.value}，该点及同段后续输出随之重算。`);
        break;
    }

    const added = f.changes.filter((c) => c.kind === 'added');
    const updated = f.changes.filter((c) => c.kind === 'updated');
    if (added.length > 0) {
      const interp = added.filter((c) => c.origin === 'interpolated').length;
      parts.push(
        `新增 ${added.length} 个输出（索引 ${compressRanges(added.map((c) => c.index))}，其中插值 ${interp} 个）。`,
      );
    }
    if (updated.length > 0) {
      parts.push(
        `改变 ${updated.length} 个历史输出（索引 ${compressRanges(updated.map((c) => c.index))}）：` +
          `这些历史结果因补点改变了滤波状态或插值输入而失效，已按版本 ${this.currentVersion} 重算。`,
      );
    }
    if (updated.length === 0) {
      parts.push(added.length > 0 ? '无历史结果被改变。' : '未改变任何历史结果。');
    }
    return parts.join('');
  }

  // ---------------------------------------------------------------- 查询接口

  /** 某索引的最新输出（缺口 void 内无输出） */
  getOutput(index: number): VersionedOutput | undefined {
    const o = this.outputs.get(index);
    return o ? { index, value: o.value, version: o.version, origin: o.origin } : undefined;
  }

  /** 全部最新输出，按索引升序 */
  getOutputs(): VersionedOutput[] {
    return [...this.outputs.entries()]
      .map(([index, o]) => ({ index, value: o.value, version: o.version, origin: o.origin }))
      .sort((p, q) => p.index - q.index);
  }

  /** 某索引的完整版本历史（哪些版本因补点改过它） */
  historyOf(index: number): VersionEntry[] {
    return (this.history.get(index) ?? []).slice();
  }

  /** 某次 addSample 的变更集 */
  getChangeSet(version: number): ChangeSet | undefined {
    return this.changeSets.find((c) => c.version === version);
  }

  /** 版本号大于 since 的全部变更集 */
  changeSetsSince(since: number): ChangeSet[] {
    return this.changeSets.filter((c) => c.version > since);
  }

  /** 当前分段视图：[段首真实样本索引, 段尾真实样本索引] */
  segments(): { start: number; end: number }[] {
    const segs: { start: number; end: number }[] = [];
    for (let i = 0; i < this.realIndices.length; i++) {
      const idx = this.realIndices[i];
      if (i === 0 || idx - this.realIndices[i - 1] - 1 > this.gapThreshold) {
        segs.push({ start: idx, end: idx });
      } else {
        segs[segs.length - 1].end = idx;
      }
    }
    return segs;
  }
}

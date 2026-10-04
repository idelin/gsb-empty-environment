/**
 * 缺口感知的信号滤波库。
 *
 * - 滤波器为有理传递函数 H(z) = B(z) / A(z)，系数由调用方输入。
 * - 相邻样本间隔 <= maxInterpolationGap 视为短缺口：按标称采样间隔线性插值补点。
 * - 相邻样本间隔 >  maxInterpolationGap 视为长缺口：断开滤波状态，下一段从零状态重启。
 * - 样本允许乱序（迟到）到达；迟到样本可能缩短缺口，甚至把长缺口补成短缺口。
 *   由于长缺口会重置滤波状态，各段相互独立，因此重算只需覆盖受影响的一段（局部重算）。
 * - 输出逐点带版本号；每次补点返回 RecomputeReport，说明哪些历史结果发生了改变。
 */

export interface FilterSpec {
  /** 前馈（分子）系数 b[0..M] */
  b: number[];
  /** 反馈（分母）系数 a[0..N]，内部归一化使 a[0] = 1 */
  a: number[];
  /** 标称采样间隔（与时间戳同单位），短缺口按此间隔插值 */
  sampleInterval: number;
  /** 缺口阈值：间隔 <= 该值线性插值；> 该值断开滤波状态 */
  maxInterpolationGap: number;
}

export interface VersionedOutput {
  /** 输出时间点（真实样本时刻或插值补点时刻） */
  t: number;
  /** 滤波输出值 */
  y: number;
  /** 该结果被最后写入时的版本号（每次 addSample 调用递增全局版本） */
  version: number;
  /** 是否为线性插值生成的补点 */
  interpolated: boolean;
}

export interface OutputChange {
  t: number;
  kind: 'added' | 'removed' | 'updated';
  before?: { y: number; version: number; interpolated: boolean };
  after?: { y: number; version: number; interpolated: boolean };
}

export interface RecomputeReport {
  /** 本次写入对应的全局版本号 */
  version: number;
  /** 本次写入的样本 */
  sample: { t: number; v: number };
  /** 是否覆盖了同时间戳的已有样本 */
  replacedExisting: boolean;
  /** 迟到样本是否把原本断开的长缺口补成了短缺口（两段合并为一段） */
  bridgedGap: boolean;
  /** 本次局部重算覆盖的时间范围 [start, end] */
  affectedRange: { start: number; end: number };
  /** 因本次补点而改变的历史结果（added/removed/updated） */
  changes: OutputChange[];
}

interface Sample {
  t: number;
  v: number;
}

/** 直接 II 型转置实现的有理滤波器，系数归一化到 a[0] = 1。 */
class RationalFilter {
  private readonly b: number[];
  private readonly a: number[];
  private readonly state: number[];

  constructor(b: number[], a: number[]) {
    if (b.length === 0 || a.length === 0) {
      throw new Error('滤波系数 b 和 a 都不能为空');
    }
    if (a[0] === 0) {
      throw new Error('a[0] 不能为 0');
    }
    const a0 = a[0];
    this.b = b.map((c) => c / a0);
    this.a = a.map((c) => c / a0);
    this.state = new Array<number>(Math.max(this.b.length, this.a.length) - 1).fill(0);
  }

  step(x: number): number {
    const { b, a, state } = this;
    const order = state.length;
    const y = b[0] * x + (order > 0 ? state[0] : 0);
    for (let i = 0; i < order - 1; i += 1) {
      const bi = i + 1 < b.length ? b[i + 1] : 0;
      const ai = i + 1 < a.length ? a[i + 1] : 0;
      state[i] = bi * x + state[i + 1] - ai * y;
    }
    if (order > 0) {
      const bm = order < b.length ? b[order] : 0;
      const am = order < a.length ? a[order] : 0;
      state[order - 1] = bm * x - am * y;
    }
    return y;
  }
}

function lowerBoundSamples(samples: Sample[], t: number): number {
  let lo = 0;
  let hi = samples.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (samples[mid].t < t) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

function lowerBoundOutputs(outputs: VersionedOutput[], t: number): number {
  let lo = 0;
  let hi = outputs.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (outputs[mid].t < t) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

export class GapAwareFilter {
  private readonly spec: FilterSpec;
  private samples: Sample[] = [];
  private outputs: VersionedOutput[] = [];
  private version = 0;

  constructor(spec: FilterSpec) {
    if (!(spec.sampleInterval > 0)) {
      throw new Error('sampleInterval 必须为正数');
    }
    if (!(spec.maxInterpolationGap > 0)) {
      throw new Error('maxInterpolationGap 必须为正数');
    }
    // 构造时校验系数（会抛出非法输入）
    new RationalFilter(spec.b, spec.a);
    this.spec = spec;
  }

  /** 当前全局版本号（每次 addSample 递增）。 */
  getVersion(): number {
    return this.version;
  }

  /** 全部输出（按时间升序的副本），逐点携带写入时的版本号。 */
  getOutputs(): VersionedOutput[] {
    return this.outputs.map((o) => ({ ...o }));
  }

  /**
   * 写入一个样本（允许乱序/迟到）。返回局部重算报告，
   * 说明哪些历史结果因本次补点而改变。
   */
  addSample(t: number, v: number): RecomputeReport {
    if (!Number.isFinite(t) || !Number.isFinite(v)) {
      throw new Error('样本时间和取值必须是有限数');
    }
    this.version += 1;

    const idx = lowerBoundSamples(this.samples, t);
    const replacedExisting =
      idx < this.samples.length && this.samples[idx].t === t;

    let bridgedGap = false;
    if (replacedExisting) {
      this.samples[idx] = { t, v };
    } else {
      const prev = this.samples[idx - 1];
      const next = this.samples[idx];
      if (prev !== undefined && next !== undefined) {
        const gap = this.spec.maxInterpolationGap;
        bridgedGap =
          next.t - prev.t > gap && t - prev.t <= gap && next.t - t <= gap;
      }
      this.samples.splice(idx, 0, { t, v });
    }

    // 受影响段的边界：向两侧扩展，直到遇到长缺口（状态断点）。
    // 长缺口会重置滤波状态，段与段相互独立，重算不越界。
    const gap = this.spec.maxInterpolationGap;
    let lo = idx;
    let hi = idx;
    while (lo > 0 && this.samples[lo].t - this.samples[lo - 1].t <= gap) lo -= 1;
    while (
      hi < this.samples.length - 1 &&
      this.samples[hi + 1].t - this.samples[hi].t <= gap
    ) {
      hi += 1;
    }

    const start = this.samples[lo].t;
    const end = this.samples[hi].t;
    const fresh = this.computeSegmentOutputs(lo, hi);

    // 用新结果替换输出中 [start, end] 范围内的旧结果。
    const oStart = lowerBoundOutputs(this.outputs, start);
    let oEnd = oStart;
    while (oEnd < this.outputs.length && this.outputs[oEnd].t <= end) oEnd += 1;
    const oldSlice = this.outputs.slice(oStart, oEnd);
    const { merged, changes } = this.mergeSlice(oldSlice, fresh);
    this.outputs.splice(oStart, oEnd - oStart, ...merged);

    return {
      version: this.version,
      sample: { t, v },
      replacedExisting,
      bridgedGap,
      affectedRange: { start, end },
      changes,
    };
  }

  /** 重算样本区间 [lo, hi]（同一段内）的全部输出，段首从零状态启动。 */
  private computeSegmentOutputs(lo: number, hi: number): VersionedOutput[] {
    const filter = new RationalFilter(this.spec.b, this.spec.a);
    const { sampleInterval } = this.spec;
    const out: VersionedOutput[] = [];
    for (let i = lo; i <= hi; i += 1) {
      const s = this.samples[i];
      if (i > lo) {
        const prev = this.samples[i - 1];
        const dt = s.t - prev.t;
        // 段内保证 dt <= maxInterpolationGap，按标称间隔线性插值补点。
        const steps = Math.round(dt / sampleInterval);
        for (let j = 1; j < steps; j += 1) {
          const frac = j / steps;
          const ti = prev.t + frac * dt;
          const vi = prev.v + frac * (s.v - prev.v);
          out.push({
            t: ti,
            y: filter.step(vi),
            version: this.version,
            interpolated: true,
          });
        }
      }
      out.push({
        t: s.t,
        y: filter.step(s.v),
        version: this.version,
        interpolated: false,
      });
    }
    return out;
  }

  /**
   * 按时间归并旧切片与新切片：
   * - 值未变的点保留旧对象（版本号不变）；
   * - 新增/删除/值变化的点记入 changes，新点使用当前版本号。
   */
  private mergeSlice(
    oldSlice: VersionedOutput[],
    fresh: VersionedOutput[],
  ): { merged: VersionedOutput[]; changes: OutputChange[] } {
    const merged: VersionedOutput[] = [];
    const changes: OutputChange[] = [];
    let i = 0;
    let j = 0;
    while (i < oldSlice.length || j < fresh.length) {
      const oldP = i < oldSlice.length ? oldSlice[i] : undefined;
      const newP = j < fresh.length ? fresh[j] : undefined;
      if (oldP !== undefined && (newP === undefined || oldP.t < newP.t)) {
        changes.push({
          t: oldP.t,
          kind: 'removed',
          before: { y: oldP.y, version: oldP.version, interpolated: oldP.interpolated },
        });
        i += 1;
      } else if (newP !== undefined && (oldP === undefined || newP.t < oldP.t)) {
        changes.push({
          t: newP.t,
          kind: 'added',
          after: { y: newP.y, version: newP.version, interpolated: newP.interpolated },
        });
        merged.push(newP);
        j += 1;
      } else if (oldP !== undefined && newP !== undefined) {
        if (oldP.y === newP.y && oldP.interpolated === newP.interpolated) {
          merged.push(oldP); // 结果未变：保留原版本号
        } else {
          changes.push({
            t: newP.t,
            kind: 'updated',
            before: { y: oldP.y, version: oldP.version, interpolated: oldP.interpolated },
            after: { y: newP.y, version: newP.version, interpolated: newP.interpolated },
          });
          merged.push(newP);
        }
        i += 1;
        j += 1;
      }
    }
    return { merged, changes };
  }
}

/** 把重算报告格式化为人类可读的说明（哪些历史结果因补点而改变）。 */
export function formatReport(report: RecomputeReport): string {
  const lines: string[] = [];
  const { start, end } = report.affectedRange;
  lines.push(
    `版本 ${report.version}：写入样本 t=${report.sample.t}, v=${report.sample.v}` +
      (report.replacedExisting ? '（覆盖同刻已有样本）' : '') +
      (report.bridgedGap ? '（迟到样本将长缺口补为短缺口，两段已合并）' : ''),
  );
  lines.push(`局部重算范围：[${start}, ${end}]`);
  if (report.changes.length === 0) {
    lines.push('无历史结果改变。');
    return lines.join('\n');
  }
  const added = report.changes.filter((c) => c.kind === 'added');
  const removed = report.changes.filter((c) => c.kind === 'removed');
  const updated = report.changes.filter((c) => c.kind === 'updated');
  lines.push(
    `历史结果变化：新增 ${added.length} 点，删除 ${removed.length} 点，更新 ${updated.length} 点`,
  );
  for (const c of report.changes) {
    if (c.kind === 'updated' && c.before && c.after) {
      lines.push(
        `  更新 t=${c.t}: ${c.before.y} (v${c.before.version}) -> ${c.after.y} (v${c.after.version})`,
      );
    } else if (c.kind === 'added' && c.after) {
      lines.push(
        `  新增 t=${c.t}: ${c.after.y} (v${c.after.version})${c.after.interpolated ? ' [插值补点]' : ''}`,
      );
    } else if (c.kind === 'removed' && c.before) {
      lines.push(
        `  删除 t=${c.t}: ${c.before.y} (v${c.before.version})${c.before.interpolated ? ' [插值补点]' : ''}`,
      );
    }
  }
  return lines.join('\n');
}

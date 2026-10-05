/**
 * 缺口 tolerant 信号滤波库 —— 公共类型定义。
 *
 * 语义总览：
 * - 样本按整数索引（采样序号）到达，允许乱序（迟到样本）。
 * - 相邻真实样本之间缺失的样本数称为“缺口”。
 *   缺口 ≤ gapThreshold 时线性插值补齐，滤波状态连续；
 *   缺口 > gapThreshold 时滤波状态断开，新段从零状态重启。
 * - 每次 addSample 调用产生一个新版本；因补点而改变的历史结果
 *   以 ChangeSet 形式返回并留档，可查询任意索引的版本历史。
 */

/** 输出点的来源：真实样本 / 缺口内线性插值 */
export type OutputOrigin = 'real' | 'interpolated';

/** 有理（ARMA/IIR）滤波系数：y[n] = (Σ bᵢ·x[n−i] − Σ aⱼ·y[n−j]) / a₀ */
export interface RationalCoefficients {
  /** 分子系数 [b₀, b₁, …, b_m]，对应 x[n] … x[n−m] */
  b: number[];
  /** 分母系数 [a₀, a₁, …, a_p]，对应 y[n] … y[n−p]；a₀ 会自动归一化 */
  a: number[];
}

export interface GapFilterConfig extends RationalCoefficients {
  /** 可线性插值的最大缺口（缺失样本个数），超过则断开滤波状态 */
  gapThreshold: number;
}

/** 带版本号的滤波输出 */
export interface VersionedOutput {
  index: number;
  value: number;
  /** 该值最后由哪个版本确定 */
  version: number;
  origin: OutputOrigin;
}

/** 单条输出变更记录 */
export interface OutputChange {
  index: number;
  kind: 'added' | 'updated';
  origin: OutputOrigin;
  oldValue?: number;
  newValue: number;
  oldVersion?: number;
  newVersion: number;
}

/** 变更原因 */
export type ChangeCause =
  | 'initial'      // 首个样本
  | 'append'       // 尾部追加
  | 'late-sample'  // 迟到样本，仅引起局部插值/状态重算
  | 'gap-bridged'  // 迟到样本把长缺口拆成短缺口，两个滤波段被桥接
  | 'correction';  // 已有索引的样本值被修正

/** 一次 addSample 产生的版本化变更集 */
export interface ChangeSet {
  version: number;
  cause: ChangeCause;
  /** 本次写入的样本 */
  sample: { index: number; value: number };
  /** 本次局部重算覆盖的网格范围（含未发生数值变化的点） */
  affectedRange: { from: number; to: number } | null;
  /** 因本次补点而新增/改变的历史结果明细 */
  changes: OutputChange[];
  /** 中文可读说明：哪些历史结果因补点而改变、为什么 */
  summary: string;
}

/** 某一索引上某次版本的取值记录 */
export interface VersionEntry {
  version: number;
  value: number;
  origin: OutputOrigin;
}

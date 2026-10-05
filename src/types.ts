// 核心类型定义。仅使用可擦除 TypeScript 语法（无 enum / 参数属性），
// 以便 Node >= 23.6 直接运行，无需构建步骤。

/** 函数依赖：lhs -> rhs，二者均为已排序、去重的属性名数组。 */
export interface FD {
  lhs: string[];
  rhs: string[];
}

/** 解析后的完整问题规格。 */
export interface Spec {
  /** 全部属性，按字典序排序，最多 10 个。 */
  attributes: string[];
  /** 原始函数依赖集 F，保持输入顺序以保证可重放。 */
  fds: FD[];
  /** 分解：每个分量是一个已排序属性数组，保持输入顺序。 */
  decomposition: string[][];
}

/** tableau 中的一个符号：杰出变量 a<k> 或非杰出变量 b<行>_<列>。 */
export interface Symbol {
  /** 名称即规范形式，例如 "a3"、"b2_5"。名称即序：合并时按规则比较。 */
  name: string;
  distinguished: boolean;
}

/** 一次合并的证据（chase 的一步）。 */
export interface MergeStep {
  step: number;
  /** 触发合并的 FD（输入序号，从 0 开始）。 */
  fdIndex: number;
  fd: FD;
  /** 参与合并的两行（tableau 行号，从 0 开始，i < j）。 */
  rowA: number;
  rowB: number;
  /** 发生合并的属性列。 */
  attribute: string;
  /** 被替换掉的符号。 */
  eliminated: string;
  /** 保留的符号。 */
  kept: string;
}

export interface ChaseResult {
  /** 最终 tableau：rows[row][col] 为符号名。 */
  tableau: string[][];
  merges: MergeStep[];
  /** 无损连接：某一行全为杰出变量。 */
  lossless: boolean;
  /** 全杰出行的行号；若不存在为 -1。 */
  witnessRow: number;
}

export interface FdPreservation {
  fdIndex: number;
  fd: FD;
  preserved: boolean;
  /** 在 G = ∪ π_Ri(F) 下从 lhs 可达的属性闭包（含 lhs）。 */
  reachableClosure: string[];
  /** 在原始 F 下的 lhs 闭包（含 lhs）。 */
  fullClosure: string[];
  /** 闭包缺口：fullClosure 中无法由 G 推出的属性。 */
  closureGap: string[];
  /** 缺口中属于该 FD 右部的属性（直接违约证据）。 */
  missingRhs: string[];
}

export interface CheckReport {
  spec: Spec;
  chase: ChaseResult;
  preservation: FdPreservation[];
  /** 所有 FD 均被保持。 */
  dependenciesPreserved: boolean;
}

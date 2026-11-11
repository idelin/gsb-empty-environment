/**
 * 正则 AST 与分析报告的公共类型定义。
 *
 * 支持的语法子集（不含反向引用）：
 *   字面量字符、转义字面量、括号分组、并集 `|`、连接、星号 `*`、
 *   以及整串锚定 `^` 与 `$`（仅允许分别出现在首尾）。
 */

export type NodeType =
  | 'literal'
  | 'concat'
  | 'alt'
  | 'group'
  | 'star';

export interface BaseNode {
  readonly type: NodeType;
  /** 该节点在源模式中的字符区间，便于审查宿主定位规则位置。 */
  readonly span: Readonly<{ start: number; end: number }>;
}

export interface LiteralNode extends BaseNode {
  readonly type: 'literal';
  readonly char: string;
}

export interface ConcatNode extends BaseNode {
  readonly type: 'concat';
  readonly parts: readonly RegexNode[];
}

export interface AltNode extends BaseNode {
  readonly type: 'alt';
  readonly options: readonly RegexNode[];
}

export interface GroupNode extends BaseNode {
  readonly type: 'group';
  readonly capture: boolean;
  readonly child: RegexNode;
}

export interface StarNode extends BaseNode {
  readonly type: 'star';
  readonly child: RegexNode;
}

export type RegexNode =
  | LiteralNode
  | ConcatNode
  | AltNode
  | GroupNode
  | StarNode;

export interface ParsedPattern {
  readonly ast: RegexNode;
  /** 模式是否以 `^` 开头。 */
  readonly anchoredStart: boolean;
  /** 模式是否以 `$` 结尾。 */
  readonly anchoredEnd: boolean;
  /** 去掉首尾锚点与定界符后的内部模式文本。 */
  readonly body: string;
  /** 原始输入文本。 */
  readonly source: string;
}

export type AnalysisStatus =
  /** 已构造见证并经回溯解释器实测确认指数级分支增长。 */
  | 'confirmed'
  /** 模式存在可泵送的歧义结构，但实测在当前预算内未观察到指数增长。 */
  | 'unverified'
  /** 未找到可泵送歧义路径的证据。注意：这不等于“安全”。 */
  | 'no-evidence';

export interface GrowthSample {
  /** 泵送次数，即重复段被拼接的次数。 */
  readonly pumpCount: number;
  /** 本次喂给回溯解释器的完整输入。 */
  readonly input: string;
  /** 解释器消耗的步数。 */
  readonly steps: number;
  /** 是否命中步数上限（未完成匹配即被截断）。 */
  readonly capped: boolean;
  /** 与上一个泵送次数样本的步数比值，首个样本为 null。 */
  readonly ratioToPrevious: number | null;
}

export interface AmbiguityWitness {
  /** 进入歧义循环前的前缀（本实现构造的见证此前缀通常为空串）。 */
  readonly prefix: string;
  /** 可泵送的重复段；每次重复至少存在两条不同的 NFA 接受路径。 */
  readonly pump: string;
  /** 迫使匹配失败的后缀：重复段的继续 + 一个模式字母表外的死字符。 */
  readonly failingSuffix: string;
  /** 见证中使用的死字符，模式内不存在该字符的任何转移。 */
  readonly deadChar: string;
}

export interface AnalysisOptions {
  /** 回溯解释器的步数上限，默认 1_000_000。 */
  readonly stepCap?: number;
  /** 泵送次数采样序列，默认 [0, 4, 8, 12, 16, 20, 24]。 */
  readonly pumpCounts?: readonly number[];
  /** 判定指数增长的最小相邻比值，默认 1.6。 */
  readonly exponentialRatioThreshold?: number;
  /** 判定指数增长所需的合格相邻比值数量，默认 2。 */
  readonly minExponentialRatios?: number;
  /** 乘积图搜索访问的乘积状态预算，默认 200_000。 */
  readonly productStateBudget?: number;
}

export interface AnalysisReport {
  readonly source: string;
  readonly status: AnalysisStatus;
  readonly witness: AmbiguityWitness | null;
  /** 回溯解释器实测得到的分支/步数增长证据；无见证时为空数组。 */
  readonly growth: readonly GrowthSample[];
  /** 状态判定的人类可读说明。 */
  readonly reason: string;
  /**
   * 强制免责声明：未找到证据绝不意味着模式安全。
   * 本工具只检测“可泵送歧义 + 失败后缀”这一类指数回溯，不覆盖所有 ReDoS 形态。
   */
  readonly disclaimer: string;
}

export class PatternSyntaxError extends Error {
  readonly position: number;

  constructor(message: string, position: number) {
    super(`模式语法错误（位置 ${position}）：${message}`);
    this.name = 'PatternSyntaxError';
    this.position = position;
  }
}

export class StepCapExceededError extends Error {
  readonly cap: number;

  constructor(cap: number) {
    super(`回溯步数超过上限 ${cap}`);
    this.name = 'StepCapExceededError';
    this.cap = cap;
  }
}

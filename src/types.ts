// 仅实现的 schema 子集（“只承诺实现的 schema 子集”）：
//   object(properties/required)、string、integer、number、boolean
//   enum、nullable、跨字段 requires、跨字段 compare
// 任何子集之外的关键字在 compileSchema 阶段即显式报错。

export type PrimitiveType = 'string' | 'integer' | 'number' | 'boolean' | 'object';

export interface PropertySchema {
  type?: PrimitiveType;
  enum?: (string | number)[];
  nullable?: boolean;
  properties?: Record<string, PropertySchema>;
  required?: string[];
  requires?: string[]; // 跨字段约束：当前字段存在 => 列出的字段必须存在
  compare?: CompareRule[]; // 跨字段比较
}

export interface RootSchema extends PropertySchema {}

export interface CompareRule {
  field: string;   // 同层另一字段
  op: '<' | '<=' | '>' | '>=';
  // 仅当 field 与 self 都声明了相同枚举（有序集合）时，枚举间比较合法
}

export interface JsonObject {
  [key: string]: unknown;
}

// 校验错误：同时给实例路径与规则路径
export interface ValidationError {
  kind:
    | 'TYPE_MISMATCH'
    | 'NULL_NOT_ALLOWED'
    | 'ENUM_MISMATCH'
    | 'REQUIRED_MISSING'
    | 'REQUIRES_MISSING'
    | 'COMPARE_FAILED';
  instancePath: string; // JSON Pointer 风格，如 /order/minAmount
  rulePath: string;     // schema 中的规则位置，如 /properties/order/type
  message: string;
  // 跨字段比较附带信息
  side?: 'self' | 'other' | 'both';
  otherPath?: string;
  op?: string;
}

export type Certainty = 'certain' | 'human';

// 单个基本修复候选（补丁）
export interface PatchCandidate {
  targetPath: string;     // 修改的实例路径（缺失字段也用其未来路径）
  op: 'set' | 'unset';
  value?: unknown;        // set 的前置值
  preconditionValue:
    | { present: true; value: unknown }
    | { present: false }; // 应用前置值：仅当实例当前值与之相等/同样缺失才允许应用
  certainty: Certainty;   // 确定 / 需人工选择
  cost: number;
  originRulePath: string; // 来源规则路径（建议不依赖隐藏默认值，可追溯）
  reason: string;         // 中文修复理由
  pending?: boolean;      // 值未知、需人工提供（不进入自动应用）
  fixes: string[];        // 该候选预期可消除的错误键
}

// 同一目标路径上互斥的候选项（一个路径一次只能选一个补丁）
export interface CandidateGroup {
  targetPath: string;
  candidates: PatchCandidate[];
}

export interface ChosenPatch {
  targetPath: string;
  op: 'set' | 'unset';
  value?: unknown;
  certainty: Certainty;
  cost: number;
  originRulePath: string;
  reason: string;
  pending?: boolean;
}

export type SearchStatus =
  | 'valid'                  // 输入本身合法，无需修复
  | 'optimal-proven'         // 有限实例已穷举全部基本修复，最优性已核验
  | 'optimal-pending-human'  // 最优计划含 pending（值必须人工提供）
  | 'no-candidate'           // 存在错误，但无任何基本修复可组合出合法结果
  | 'budget-exhausted';      // 预算中断：返回可行上界与未决信息

export interface RepairPlan {
  patches: ChosenPatch[];
  totalCost: number;
  allCertain: boolean;
  containsPending: boolean;
  status: 'legal' | 'pending-human';
}

export interface RepairSuggestionResult {
  valid: boolean;
  errors: ValidationError[];
  status: SearchStatus;
  groups: CandidateGroup[];
  bestPlans: RepairPlan[];      // 保留多种同成本选择（平局全部保留）
  optimalCost: number | null;   // 已核验最优代价
  upperBoundCost: number | null;// 预算中断时的可行上界
  pendingGroups: string[];      // 未决：目标路径含需人工提供的值
  unexploredGroups: string[];   // 预算中断时未完整展开的候选组
  budget: { used: number; limit: number };
  combinationsTotal: string;    // 全部组合数（BigInt 序列化字符串）
  combinationsChecked: number;
}

export interface ApplyResult {
  ok: boolean;
  output?: JsonObject;
  errors: ValidationError[];
  failedPatch?: { targetPath: string; reason: string };
  rolledBack: boolean;
}

// 预算中断时哪些候选组尚未完整展开

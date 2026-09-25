/**
 * 公共类型定义。
 *
 * 输入是 JSON 兼容树：对象键无序、数组有序。
 * 规则通过 RFC 6901 风格的 JSON Pointer 模式定位字段，
 * 其中单个段 "*" 匹配任意单个键/下标段。
 */

export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export type JsonObject = { [key: string]: JsonValue };

/** 规则的声明顺序即管道应用顺序；数组下标越小越早执行。 */
export type RuleType = "whitespace" | "date" | "enum";

/**
 * 空白规则。
 * - trim:     去除首尾空白
 * - collapse: 在 trim 基础上把内部连续空白折叠为单个空格
 * null 永远不会被等同处理：该规则只作用于字符串。
 */
export interface WhitespaceRule {
  id: string;
  type: "whitespace";
  /** 例如 "/user/name"、"/items/[*]/note"。 */
  path: string;
  mode: "trim" | "collapse";
}

/**
 * 日期规则。
 * formats 中按声明顺序严格尝试；全部失败则该字段出错并保留原值。
 * 两种格式都能解析但得到不同日期时判定为歧义（拒绝猜测年月顺序）。
 * 输出统一为 outputFormat。
 */
export interface DateRule {
  id: string;
  type: "date";
  path: string;
  /** strftime 风格，仅支持 YYYY MM DD HH mm ss 与字面分隔符。 */
  formats: string[];
  outputFormat: string;
  /** 幂等反例搜索时使用的年份（默认 2020、2021、2024，闰年覆盖 2024）。 */
  probeYears?: number[];
}

/**
 * 枚举别名规则。
 * map 的键是可接受的别名（原样精确匹配），值是规范化目标。
 * 命中即替换；未命中时 unknown 决定报错保留还是原样保留。
 */
export interface EnumRule {
  id: string;
  type: "enum";
  path: string;
  map: Record<string, string>;
  onUnknown?: "error" | "keep";
}

export type Rule = WhitespaceRule | DateRule | EnumRule;

export interface RuleSet {
  /** 规则集名称。 */
  name: string;
  /** 规则数组；数组顺序即显式执行顺序。 */
  rules: Rule[];
}

/** 单个规则在单个字段上的一步变更。 */
export interface RuleChange {
  ruleId: string;
  ruleType: RuleType;
  from: string;
  to: string;
}

/** 一个字段（叶子路径）的完整轨迹。 */
export interface FieldRecord {
  /** 规范化前的字段身份。 */
  before: FieldIdentity;
  /** 规范化后的字段身份。 */
  after: FieldIdentity;
  /** 逐步变更（空数组表示未变化或被跳过）。 */
  changes: RuleChange[];
  /** 是否最终保持原值（含出错保留、未命中保留、本来就相等）。 */
  unchanged: boolean;
  /** 该字段上发生的错误（至多一个；出错后后续规则不再作用于该字段）。 */
  error?: FieldError;
}

/** 字段身份：路径 + 值指纹。规范化只改值不改路径，身份通过指纹区分。 */
export interface FieldIdentity {
  path: string;
  value: JsonValue;
  kind: "string" | "null" | "other";
  /** 稳定指纹，便于在版本间比对字段身份。 */
  fingerprint: string;
}

export interface FieldError {
  path: string;
  ruleId: string;
  ruleType: RuleType;
  code: "DATE_UNPARSEABLE" | "DATE_AMBIGUOUS" | "ENUM_UNKNOWN";
  message: string;
  /** 出错时保留下来的原始值。 */
  retained: JsonValue;
}

/** RFC 6902 风格的逆补丁（仅 replace），apply 后恢复规范化前的值。 */
export type InverseOp =
  | { op: "replace"; path: string; value: JsonValue };

export interface NormalizeResult {
  output: JsonValue;
  /** 以字段路径为键的逐字段记录。 */
  records: Record<string, FieldRecord>;
  /** 可直接定位到路径与规则的错误列表。 */
  errors: FieldError[];
  /** 逆补丁：按顺序应用到 output 可还原 input。 */
  inversePatch: InverseOp[];
  rulesetVersion: string | null;
}

/** 两条规则在值维度上可能发生先后触发（A 的输出可能成为 B 的输入）。 */
export interface TriggerEdge {
  from: string;
  to: string;
  reason: string;
  witness: string;
}

export interface RuleDependency {
  ruleId: string;
  ruleType: RuleType;
  order: number;
  path: string;
  /** 读路径：规则自身声明的模式。 */
  reads: string[];
  /** 写路径：与读相同的定位（原地改值）。 */
  writes: string[];
  /** 声明的有限输入域。 */
  declaredDomain: string[];
}

export interface AliasConflict {
  paths: string[];
  rules: string[];
  alias: string;
  canonical: string[];
  message: string;
}

export interface CycleReport {
  rules: string[];
  message: string;
}

export interface OrderViolation {
  edge: TriggerEdge;
  message: string;
}

export interface RuleSetAnalysis {
  ok: boolean;
  order: string[];
  dependencies: RuleDependency[];
  /** 路径模式两两是否可能相交（* 展开为同一代表段）。 */
  overlapPairs: Array<[string, string]>;
  aliasConflicts: AliasConflict[];
  triggerEdges: TriggerEdge[];
  cycles: CycleReport[];
  orderViolations: OrderViolation[];
  duplicateIds: string[];
  errors: string[];
}

export type IdempotencyStatus = "proved" | "refuted" | "undecided";

export interface Counterexample {
  /** 触发现象的输入值（在代表路径上）。 */
  input: string;
  /** 第一次规范化输出。 */
  once: string;
  /** 第二次规范化输出（与 once 不同即破坏幂等）。 */
  twice: string;
}

export interface IdempotencyReport {
  status: IdempotencyStatus;
  /** 搜索的有限域大小。 */
  domainSize: number;
  /** 实际尝试的取值数量（预算耗尽时小于 domainSize）。 */
  evaluated: number;
  budget: number;
  counterexamples: Counterexample[];
  note: string;
}

/**
 * 差分判定包的公共类型定义。
 *
 * 设计约束：
 * - 每个类型节点都绑定原始字节区间（ByteRange），重排集合时区间随节点一起移动，绝不重绑。
 * - 只有显式 `ordered === false` 的复合节点才允许按语义指纹重排。
 * - 接受/拒绝是不可归并的两种结局，由 compare 层先裁决，再进入树比较。
 */

/** 半开字节区间 [start, end)，指向输入 Uint8Array 的下标。 */
export interface ByteRange {
  start: number;
  end: number;
}

/** 解析失败类别，由调用方把各自实现的错误映射到这组统一类别。 */
export type ErrorCategory =
  | "malformed"
  | "truncated"
  | "invalid_value"
  | "cardinality"
  | "unsupported"
  | "internal";

export interface ParseError {
  category: ErrorCategory;
  message?: string;
  /** 错误被检测到的原始字节位置（可选）。 */
  range?: ByteRange;
  /** 实现侧自带的错误码（可选，仅作证据）。 */
  code?: string;
  /** 由 compare 层在回调抛异常时打上的标记。 */
  thrown?: boolean;
}

/** 解析回调的返回值：接受或拒绝，二选一。 */
export type ParseOutcome =
  | {
      accepted: true;
      /** 实际消费的字节数。 */
      consumed: number;
      tree: TypeNode;
    }
  | {
      accepted: false;
      error: ParseError;
      /** 拒绝前已消费的字节数（若实现可提供）。 */
      consumed?: number;
      /** 拒绝前已构建的部分类型树（可选）。 */
      tree?: TypeNode;
    };

export type ParserCallback = (input: Uint8Array) => ParseOutcome;

/** 重复字段/集合元素的包装节点；同名子节点可以重复出现。 */
export interface ChildNode {
  /** 字段名、tag 或 map key；重复字段允许重名。 */
  name?: string;
  node: TypeNode;
}

export interface TypeNode {
  /** 协议类型名，如 "VarInt"、"HttpHeader"。 */
  type: string;
  /** 语义值；支持 bigint、Uint8Array、数组、普通对象、Map、Set。 */
  value?: unknown;
  /** 该节点在原始字节流中的区间，必填，随节点终生绑定。 */
  range: ByteRange;
  /** 原始编码的留痕（hex、注解形式等），用于"等值不同编码"判定。 */
  encoding?: string;
  /**
   * 复合节点的子节点。
   * 默认按有序集合处理；只有显式写 ordered === false 才允许重排。
   */
  children?: ChildNode[];
  /** 仅对复合节点有效；省略或 true 表示有序，false 表示显式无序集合。 */
  ordered?: boolean;
}

export type DivergenceCategory =
  | "acceptance_mismatch"
  | "reject_category_mismatch"
  | "error_location_mismatch"
  | "type_mismatch"
  | "value_mismatch"
  | "cardinality_mismatch"
  | "order_mismatch"
  | "consumed_length_mismatch"
  | "encoding_mismatch"
  | "range_mismatch";

export type Severity = "semantic" | "structural" | "informational";

/** 从根到分歧节点的最小路径段。 */
export interface PathSegment {
  /** 字段/tag/map key 名（重复字段可能重名）。 */
  name?: string;
  /** 在父节点 children 中的原始下标（重排前的位置）。 */
  index: number;
  /** 该位置节点的类型名。 */
  type: string;
}

/** 单方证据，只携带该侧自己的字节区间与编码，不做跨侧重绑。 */
export interface SideEvidence {
  type: string;
  value?: unknown;
  encoding?: string;
  range?: ByteRange;
  error?: ParseError;
  consumed?: number;
}

export interface Divergence {
  category: DivergenceCategory;
  severity: Severity;
  /** 是否构成语义分歧；informational/structural 项为 false。 */
  semantic: boolean;
  /** 最小分歧路径；空数组表示分歧发生在根/顶层。 */
  path: PathSegment[];
  /** 左侧证据；该侧缺失对应对象时为 null。 */
  left: SideEvidence | null;
  /** 右侧证据；该侧缺失对应对象时为 null。 */
  right: SideEvidence | null;
  detail: string;
}

export interface SideSummary {
  accepted: boolean;
  consumed?: number;
  error?: ParseError;
}

export interface ComparisonResult {
  inputLength: number;
  left: SideSummary;
  right: SideSummary;
  /** 拒绝侧没有树时为 null。 */
  trees: { left: TypeNode | null; right: TypeNode | null };
  /**
   * 分歧列表。每条分歧都位于最小路径上：
   * 一旦某节点产生分歧就不再向下展开，无序集合中每个无匹配键各占一条。
   */
  divergences: Divergence[];
  /** 语义等价：不存在任何 semantic === true 的分歧（编码/区间等信息项不影响）。 */
  equivalent: boolean;
}

/**
 * 条件语法树与字段绑定检查的公共类型定义。
 *
 * 条件 AST 仅覆盖 Falco 条件表达式的布尔骨架：
 * 字段比较、字面量、and / or / not，以及待展开的宏引用。
 */

/** 比较与集合运算符。 */
export type Operator =
  | '='
  | '!='
  | '=='
  | 'contains'
  | 'icontains'
  | 'startswith'
  | 'endswith'
  | '>'
  | '>='
  | '<'
  | '<='
  | 'in';

/** 字面量值：字符串、数字、布尔或 null（null 语义上表示空值）。 */
export type LiteralValue = string | number | boolean | null;

/** 条件语法树节点。 */
export type ConditionNode =
  | CompareNode
  | LiteralNode
  | AndNode
  | OrNode
  | NotNode
  | MacroRefNode;

/** 字段比较节点，如 `fd.name = "/etc/passwd"`。 */
export interface CompareNode {
  kind: 'compare';
  field: string;
  operator: Operator;
  value: LiteralValue;
}

/** 裸字面量节点，如条件里出现的常量 `true`。 */
export interface LiteralNode {
  kind: 'literal';
  value: LiteralValue;
}

/** 逻辑与。 */
export interface AndNode {
  kind: 'and';
  operands: ConditionNode[];
}

/** 逻辑或。 */
export interface OrNode {
  kind: 'or';
  operands: ConditionNode[];
}

/** 逻辑非。 */
export interface NotNode {
  kind: 'not';
  operand: ConditionNode;
}

/** 宏引用节点，展开前以名称占位。 */
export interface MacroRefNode {
  kind: 'macro';
  name: string;
}

/**
 * 字段到可用事件类型的映射。
 * 键为字段名，值为该字段能取到非空值的 evt.type 列表。
 */
export type FieldAvailabilityMap = Record<string, readonly string[]>;

/** 检查发现的问题类别。 */
export type FindingCode =
  | 'never-matches'
  | 'always-true'
  | 'proc-name-truncation';

/** 单条检查发现。 */
export interface Finding {
  code: FindingCode;
  /** 触发问题的事件类型（进程名截断问题与事件类型无关，使用 null）。 */
  eventType: string | null;
  /** 涉及的字段名（若适用）。 */
  field: string | null;
  /** 人类可读的问题描述。 */
  message: string;
}

/** checkRuleCondition 的输入。 */
export interface CheckInput {
  /** 规则条件语法树，可包含未展开的宏引用。 */
  condition: ConditionNode;
  /** 规则声明的 evt.type 集合，如 `['openat', 'execve']`。 */
  eventTypes: readonly string[];
  /** 字段到可用事件类型的映射。 */
  fieldAvailability: FieldAvailabilityMap;
  /** 宏定义表：宏名到条件 AST 的映射。 */
  macros?: Record<string, ConditionNode>;
}

/** checkRuleCondition 的输出。 */
export interface CheckReport {
  /** 存在任何级别问题时为 false，规则不应直接加载。 */
  ok: boolean;
  /** 全部检查发现。 */
  findings: Finding[];
  /** 宏完全展开后的条件 AST。 */
  expandedCondition: ConditionNode;
}


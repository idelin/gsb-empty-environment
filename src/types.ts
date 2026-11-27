/**
 * AST、Schema 输入、安全策略与校验结果的零依赖类型定义。
 */

export interface SourceLocation {
  readonly start: number;
  readonly end: number;
  readonly line: number;
  readonly column: number;
}

export const TokenKind = {
  EOF: 'EOF',
  PUNCTUATOR: 'PUNCTUATOR',
  NAME: 'NAME',
  INT: 'INT',
  FLOAT: 'FLOAT',
  STRING: 'STRING',
  BLOCK_STRING: 'BLOCK_STRING',
} as const;

export type TokenKind = (typeof TokenKind)[keyof typeof TokenKind];

export interface Token {
  kind: TokenKind;
  value: string;
  start: number;
  end: number;
  line: number;
  column: number;
}

export type ValueNode =
  | { kind: 'Variable'; name: string; loc: SourceLocation }
  | { kind: 'IntValue'; value: string; loc: SourceLocation }
  | { kind: 'FloatValue'; value: string; loc: SourceLocation }
  | { kind: 'StringValue'; value: string; loc: SourceLocation }
  | { kind: 'BooleanValue'; value: boolean; loc: SourceLocation }
  | { kind: 'NullValue'; loc: SourceLocation }
  | { kind: 'EnumValue'; value: string; loc: SourceLocation }
  | { kind: 'ListValue'; values: ValueNode[]; loc: SourceLocation }
  | { kind: 'ObjectValue'; fields: ObjectFieldNode[]; loc: SourceLocation };

export interface ObjectFieldNode {
  name: string;
  value: ValueNode;
  loc: SourceLocation;
}

export interface ArgumentNode {
  name: string;
  value: ValueNode;
  loc: SourceLocation;
}

export interface DirectiveNode {
  name: string;
  args: ArgumentNode[];
  loc: SourceLocation;
}

export type TypeNode =
  | { kind: 'NamedType'; name: string; loc: SourceLocation }
  | { kind: 'ListType'; type: TypeNode; loc: SourceLocation }
  | { kind: 'NonNullType'; type: TypeNode; loc: SourceLocation };

export interface VariableDefinitionNode {
  variable: string;
  type: TypeNode;
  defaultValue?: ValueNode;
  directives: DirectiveNode[];
  loc: SourceLocation;
}

export interface FieldNode {
  kind: 'Field';
  alias?: string;
  name: string;
  args: ArgumentNode[];
  directives: DirectiveNode[];
  selectionSet?: SelectionSetNode;
  loc: SourceLocation;
}

export interface FragmentSpreadNode {
  kind: 'FragmentSpread';
  name: string;
  directives: DirectiveNode[];
  loc: SourceLocation;
}

export interface InlineFragmentNode {
  kind: 'InlineFragment';
  typeCondition?: string;
  directives: DirectiveNode[];
  selectionSet: SelectionSetNode;
  loc: SourceLocation;
}

export type SelectionNode = FieldNode | FragmentSpreadNode | InlineFragmentNode;

export interface SelectionSetNode {
  kind: 'SelectionSet';
  selections: SelectionNode[];
  loc: SourceLocation;
}

export interface OperationDefinitionNode {
  kind: 'OperationDefinition';
  operation: 'query' | 'mutation' | 'subscription';
  name?: string;
  variableDefinitions: VariableDefinitionNode[];
  directives: DirectiveNode[];
  selectionSet: SelectionSetNode;
  loc: SourceLocation;
}

export interface FragmentDefinitionNode {
  kind: 'FragmentDefinition';
  name: string;
  typeCondition: string;
  directives: DirectiveNode[];
  selectionSet: SelectionSetNode;
  loc: SourceLocation;
}

export type DefinitionNode = OperationDefinitionNode | FragmentDefinitionNode;

export interface DocumentNode {
  kind: 'Document';
  definitions: DefinitionNode[];
}

/* ---------------------------------- Schema --------------------------------- */

export interface InputTypeFieldInput {
  /** 类型引用字符串，例如 "String!"、"[ID!]!"、"TenantContextInput" */
  type: string;
  /** 标记为敏感租户作用域参数，禁止客户端在查询中覆盖 */
  sensitive?: boolean;
}

export interface FieldDefinitionInput {
  type: string;
  args?: Record<string, InputTypeFieldInput>;
  /** 节点加权复杂度权重，缺省 1 */
  weight?: number;
}

export interface GraphQLTypeInput {
  kind: 'OBJECT' | 'INTERFACE' | 'UNION' | 'INPUT_OBJECT' | 'SCALAR' | 'ENUM';
  fields?: Record<string, FieldDefinitionInput>;
  inputFields?: Record<string, InputTypeFieldInput>;
  /** OBJECT/INTERFACE 实现的接口 */
  interfaces?: string[];
  /** UNION 成员或 INTERFACE 的实现类型名 */
  possibleTypes?: string[];
  enumValues?: string[];
}

export interface SchemaInput {
  queryType?: string;
  mutationType?: string;
  subscriptionType?: string;
  types: Record<string, GraphQLTypeInput>;
}

/* ---------------------------------- Policy --------------------------------- */

export interface RolePolicy {
  /** 键为 "TypeName.fieldName"，true 允许、false 拒绝 */
  fields?: Record<string, boolean>;
  /** 未显式列出的字段默认决策，缺省取全局 defaultAllow（再缺省 false） */
  defaultAllow?: boolean;
}

export interface SecurityPolicyInput {
  /** 当前请求主体的角色名，对应 policies 的键 */
  role: string;
  policies?: Record<string, RolePolicy>;
  /** 全局默认授权决策，缺省 false（白名单制） */
  defaultAllow?: boolean;
  maxDepth?: number;
  /** 完全展开后字段调用总数上限（别名放大倍数） */
  maxAliasMultiplier?: number;
  maxComplexity?: number;
  /** 列表分页参数的确定性上界，缺省 100 */
  maxListSize?: number;
  /** 作为列表乘法因子的参数名，缺省 ['first', 'limit'] */
  listFactorArgs?: string[];
  /** 阻断 __schema/__type 内省，缺省 true */
  blockIntrospection?: boolean;
  /** 解析器嵌套深度保护，缺省 200 */
  maxParseNesting?: number;
}

/* --------------------------------- Results --------------------------------- */

export type ViolationCode =
  | 'SYNTAX_ERROR'
  | 'DUPLICATE_FRAGMENT'
  | 'FRAGMENT_CYCLE'
  | 'UNKNOWN_FRAGMENT'
  | 'UNKNOWN_TYPE'
  | 'UNKNOWN_FIELD'
  | 'FIELD_FORBIDDEN'
  | 'INTROSPECTION_BLOCKED'
  | 'SENSITIVE_ARG_OVERRIDE'
  | 'VARIABLE_NOT_DEFINED'
  | 'VARIABLE_TYPE_MISMATCH'
  | 'VARIABLE_REQUIRED'
  | 'INVALID_LIST_ARGUMENT'
  | 'LIMIT_DEPTH_EXCEEDED'
  | 'LIMIT_ALIAS_EXCEEDED'
  | 'LIMIT_COMPLEXITY_EXCEEDED';

export interface ComplexityBounds {
  maxDepth: number;
  aliasMultiplier: number;
  aliasedFieldCount: number;
  complexityUpperBound: number;
}

export interface Violation {
  code: ViolationCode;
  message: string;
  /** 规范路径，例如 "Query.users.User.tenantToken" */
  path: string;
  /** 命中该违规时经过的具名片段展开链 */
  fragmentChain: string[];
  /** 限额类违规携带的确定性复杂度上界快照 */
  bounds?: ComplexityBounds;
}

export interface ValidationResult extends ComplexityBounds {
  ok: boolean;
  violations: Violation[];
}

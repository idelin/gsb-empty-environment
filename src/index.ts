/**
 * 零依赖 GraphQL 静态安全组件统一入口。
 *
 * 典型用法：
 *
 *   import { parseGraphQL, validateGraphQL, SecurityValidator } from './src/index.ts';
 *
 *   const result = validateGraphQL(schema, policy, queryText);
 *   if (!result.ok) {
 *     // 执行前硬拒绝：result.violations 含规范路径、片段链与复杂度上界
 *   }
 */

export { Lexer, LexError, dedentBlockString } from './lexer.ts';
export { Parser, ParseError } from './parser.ts';
export { SecurityValidator } from './validator.ts';
export { TokenKind } from './types.ts';
export {
  parseTypeRef,
  typeNodeToString,
  isVariableAssignable,
  typeNodeNamedName,
  refNamedName,
  refIsList,
  refIsNonNull,
  type ParsedTypeRef,
} from './typeRef.ts';
export {
  type ArgumentNode,
  type ComplexityBounds,
  type DocumentNode,
  type FieldNode,
  type FragmentDefinitionNode,
  type GraphQLTypeInput,
  type InlineFragmentNode,
  type OperationDefinitionNode,
  type RolePolicy,
  type SchemaInput,
  type SecurityPolicyInput,
  type SelectionSetNode,
  type ValidationResult,
  type Violation,
  type ViolationCode,
} from './types.ts';

import { Parser } from './parser.ts';
import type {
  DocumentNode,
  SchemaInput,
  SecurityPolicyInput,
  ValidationResult,
} from './types.ts';
import { SecurityValidator } from './validator.ts';

/** 仅解析查询文档为 AST，不执行任何策略校验。 */
export function parseGraphQL(source: string, maxParseNesting = 200): DocumentNode {
  return Parser.parse(source, maxParseNesting);
}

/** 解析并执行完整静态安全策略校验，返回确定性的违规清单与复杂度上界。 */
export function validateGraphQL(
  schema: SchemaInput,
  policy: SecurityPolicyInput,
  source: string,
): ValidationResult {
  return new SecurityValidator(schema, policy).validate(source);
}

import { UnauthorizedFunctionError, UnknownIdentifierError } from './errors.ts';
import type { AstNode } from './parser.ts';

export interface ValidationContext {
  /** Names of variables the expression is allowed to read. */
  variables: ReadonlySet<string>;
  /** Names of whitelisted functions the expression is allowed to call. */
  functions: ReadonlySet<string>;
}

/**
 * Statically walks the AST and rejects anything that is not explicitly
 * whitelisted. Runs before any evaluation, so unauthorized code never
 * executes a single instruction.
 */
export function validate(ast: AstNode, ctx: ValidationContext): void {
  switch (ast.kind) {
    case 'num':
    case 'str':
    case 'bool':
    case 'null':
      return;
    case 'ident':
      if (!ctx.variables.has(ast.name)) {
        throw new UnknownIdentifierError(ast.name);
      }
      return;
    case 'unary':
      validate(ast.arg, ctx);
      return;
    case 'binary':
      validate(ast.left, ctx);
      validate(ast.right, ctx);
      return;
    case 'call':
      if (!ctx.functions.has(ast.name)) {
        throw new UnauthorizedFunctionError(ast.name);
      }
      for (const arg of ast.args) validate(arg, ctx);
      return;
  }
}

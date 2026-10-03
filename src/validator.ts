import type { Expr } from './ast.ts';
import { ValidationError } from './errors.ts';

export interface ValidationPolicy {
  /** Identifiers the expression is allowed to read (tenant-supplied variables + `self`). */
  readonly allowedVariables: ReadonlySet<string>;
  /** Function names the expression is allowed to call (sandbox built-ins only). */
  readonly allowedFunctions: ReadonlySet<string>;
  /** Maximum AST nesting depth. */
  readonly maxDepth: number;
  /** Maximum total AST node count. */
  readonly maxNodes: number;
  /** Maximum string-literal length. */
  readonly maxStringLength: number;
}

export const DEFAULT_POLICY: Omit<ValidationPolicy, 'allowedVariables'> = {
  allowedFunctions: new Set([
    'min', 'max', 'abs', 'floor', 'ceil', 'round',
    'len', 'contains', 'startsWith', 'endsWith', 'lower', 'upper',
    'eval',
  ]),
  maxDepth: 256,
  maxNodes: 500,
  maxStringLength: 1024,
};

/**
 * Statically walks the AST and rejects anything outside the whitelist:
 * unknown identifiers, unauthorized function calls, oversized literals,
 * and structurally abusive (too deep / too wide) trees.
 */
export function validate(expr: Expr, policy: ValidationPolicy): void {
  let nodes = 0;

  const walk = (node: Expr, depth: number): void => {
    nodes++;
    if (nodes > policy.maxNodes) {
      throw new ValidationError(`Expression exceeds ${policy.maxNodes} AST nodes`);
    }
    if (depth > policy.maxDepth) {
      throw new ValidationError(`Expression exceeds ${policy.maxDepth} levels of nesting`);
    }

    switch (node.kind) {
      case 'Literal':
        if (typeof node.value === 'string' && node.value.length > policy.maxStringLength) {
          throw new ValidationError(
            `String literal exceeds ${policy.maxStringLength} characters`,
          );
        }
        if (typeof node.value === 'number' && !Number.isFinite(node.value)) {
          throw new ValidationError('Non-finite numeric literal');
        }
        return;
      case 'Identifier':
        if (!policy.allowedVariables.has(node.name)) {
          throw new ValidationError(`Unknown identifier '${node.name}'`);
        }
        return;
      case 'Unary':
        walk(node.operand, depth + 1);
        return;
      case 'Binary':
        walk(node.left, depth + 1);
        walk(node.right, depth + 1);
        return;
      case 'Call':
        if (!policy.allowedFunctions.has(node.callee)) {
          throw new ValidationError(
            `Call to unauthorized function '${node.callee}' is forbidden`,
          );
        }
        for (const arg of node.args) walk(arg, depth + 1);
        return;
    }
  };

  walk(expr, 1);
}

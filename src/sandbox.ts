import { quote } from './ast.ts';
import { DEFAULT_LIMITS, ResourceGuard, type Limits } from './guard.ts';
import { evaluateNode, type RuntimeEnv, type Value } from './interpreter.ts';
import { parse } from './parser.ts';
import { DEFAULT_POLICY, validate, type ValidationPolicy } from './validator.ts';

export interface EvaluateOptions {
  /** Tenant-supplied variables visible to the expression. `self` is auto-bound. */
  readonly variables?: Record<string, Value>;
  /** Per-tenant limit overrides (merged over DEFAULT_LIMITS). */
  readonly limits?: Partial<Limits>;
  /** Extra allowed function names beyond the built-in whitelist. */
  readonly extraFunctions?: readonly string[];
}

export interface EvaluateResult {
  readonly value: Value;
  readonly stepsUsed: number;
  readonly memoryUsed: number;
}

/**
 * Multi-tenant entry point: parses, statically validates, then interprets a
 * user-supplied expression under a fresh per-call ResourceGuard. Tenants are
 * isolated because no guard, environment, or AST state is shared across calls.
 */
export function evaluate(source: string, options: EvaluateOptions = {}): EvaluateResult {
  const limits: Limits = { ...DEFAULT_LIMITS, ...options.limits };
  const variables: Record<string, Value> = { ...options.variables };

  const ast = parse(source);

  // `self` exposes the expression's own AST as a quoted value, enabling
  // meta-circular formulas such as eval(self). It stays sandboxed because
  // re-evaluation shares this call's guard and policy.
  if (!('self' in variables)) {
    variables.self = quote(ast);
  }

  const policy: ValidationPolicy = {
    ...DEFAULT_POLICY,
    allowedVariables: new Set(Object.keys(variables)),
    allowedFunctions: new Set([
      ...DEFAULT_POLICY.allowedFunctions,
      ...(options.extraFunctions ?? []),
    ]),
  };
  validate(ast, policy);

  const guard = new ResourceGuard(limits);
  const env: RuntimeEnv = { variables, guard, policy };
  const value = evaluateNode(ast, env, 0);

  return { value, stepsUsed: guard.stepsUsed, memoryUsed: guard.memoryUsed };
}

export { quote } from './ast.ts';
export {
  BudgetExceeded,
  MemoryLimitExceeded,
  ParseError,
  TimeoutExceeded,
  ValidationError,
} from './errors.ts';
export { DEFAULT_LIMITS, type Limits } from './guard.ts';
export type { Value } from './interpreter.ts';

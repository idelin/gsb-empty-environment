import { compile, type Graph } from './compiler.ts';
import { validate, type ValidationResult, type ValidateOptions } from './validator.ts';
import { interpret, type InterpretOptions } from './interpreter.ts';
import type { Schema } from './schema.ts';

export { compile, CompileError, type Graph, type Node } from './compiler.ts';
export {
  validate,
  collectFrom,
  errorKey,
  type ValidationError,
  type ValidationResult,
  type ValidateOptions,
  type Verdict,
} from './validator.ts';
export { interpret, type InterpretOptions, type InterpretResult } from './interpreter.ts';
export { InstanceStore } from './store.ts';
export type { Schema } from './schema.ts';

export interface CrossCheckResult {
  /** True when both engines completed and agreed. */
  consistent: boolean;
  graph: ValidationResult;
  interpreter: ReturnType<typeof interpret>;
}

/**
 * Cross-checks the compiled graph validator against the independent
 * interpreter. When the interpreter's budget is exhausted its verdict is
 * 'incomplete' and the check is skipped (an exhausted budget is never
 * treated as a schema failure, nor as a disagreement).
 */
export function crossCheck(
  graph: Graph,
  rootSchema: Schema,
  value: unknown,
  validateOpts: ValidateOptions = {},
  interpretOpts: InterpretOptions = {},
): CrossCheckResult {
  const graphResult = validate(graph, value, validateOpts);
  const interpResult = interpret(rootSchema, value, interpretOpts);
  const comparable = interpResult.verdict !== 'incomplete' && graphResult.verdict !== 'incomplete';
  const consistent = !comparable || interpResult.verdict === graphResult.verdict;
  return { consistent, graph: graphResult, interpreter: interpResult };
}

export function compileAndCheck(rootSchema: Schema): Graph {
  return compile(rootSchema);
}

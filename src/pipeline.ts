import { RecordError, TransformError } from './errors.ts';
import type { JsonObject, JsonPath } from './json.ts';

export interface StepContext {
  /** Id of the record currently flowing through the pipeline. */
  readonly recordId: string;
  /** Zero-based index of the record in the input stream. */
  readonly recordIndex: number;
}

/**
 * One named transform step. `apply` receives the record (a JSON tree) and
 * returns the transformed tree. Throw {@link RecordError} (or `failAt`) to
 * fail with a precise path; any other throw is reported at the tree root.
 */
export interface Step {
  readonly name: string;
  apply(record: JsonObject, ctx: StepContext): JsonObject;
}

export function defineStep(
  name: string,
  apply: (record: JsonObject, ctx: StepContext) => JsonObject,
): Step {
  return { name, apply };
}

/**
 * Run a record through the steps in order. The first failing step aborts
 * the record with a {@link TransformError} that names the record, the step,
 * and the JSON path of the offending value.
 */
export function runPipeline(
  record: JsonObject,
  steps: readonly Step[],
  ctx: StepContext,
): JsonObject {
  let current = record;
  for (const step of steps) {
    try {
      current = step.apply(current, ctx);
    } catch (err) {
      const path: JsonPath = err instanceof RecordError ? err.path : [];
      const message = err instanceof Error ? err.message : String(err);
      throw new TransformError({
        recordId: ctx.recordId,
        step: step.name,
        path,
        message,
        cause: err,
      });
    }
  }
  return current;
}

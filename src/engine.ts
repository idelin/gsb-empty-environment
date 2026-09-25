import { MigrationError } from "./errors.ts";
import type { MigrationStep, VersionGraph } from "./graph.ts";
import {
  checkJsonCompatible,
  deepClone,
  hashJson,
  type JsonValue,
} from "./json.ts";
import type { Journal, JournalStore } from "./journal.ts";
import { validate } from "./schema.ts";

export interface PublishRecord {
  readonly version: string;
  readonly data: JsonValue;
}

export interface MigrateOptions {
  readonly graph: VersionGraph;
  readonly from: string;
  readonly to: string;
  readonly input: JsonValue;
  /** Checkpoint store. When omitted, no checkpoints are kept. */
  readonly journal?: JournalStore;
  /**
   * Called exactly once, only after every step succeeded and the final
   * document validates. A failed migration never publishes a new version.
   */
  readonly publish?: (record: PublishRecord) => void;
}

export interface MigrateResult {
  readonly version: string;
  readonly data: JsonValue;
  /** Steps executed during THIS run (excludes steps restored from journal). */
  readonly executedSteps: string[];
  /** Steps skipped because the journal proved them already done. */
  readonly skippedSteps: string[];
}

export class MigrationEngine {
  migrate(options: MigrateOptions): MigrateResult {
    const { graph, from, to, journal } = options;

    // 1. The input must be a JSON-compatible tree; errors carry paths.
    const jsonIssues = checkJsonCompatible(options.input);
    if (jsonIssues.length > 0) {
      throw new MigrationError(
        "INVALID_JSON_TREE",
        jsonIssues.map((i) => `${i.path}: ${i.message}`).join("; "),
        { issues: jsonIssues, path: jsonIssues[0].path },
      );
    }
    const input = deepClone(options.input);

    // 2. Deterministic route selection; equal-cost ties are rejected.
    const route = graph.findPath(from, to);
    const plan = route.steps;

    // 3. Resume: verify the original input and the executed-step prefix.
    let state: JsonValue = input;
    let startIndex = 0;
    const skipped: string[] = [];
    const prior = journal?.load() ?? null;
    if (prior) {
      startIndex = this.verifyCheckpoint(prior, input, plan, from, to);
      state = deepClone(prior.state);
      skipped.push(...prior.completedSteps);
    } else if (journal) {
      journal.save({
        from,
        to,
        inputHash: hashJson(input),
        completedSteps: [],
        state: deepClone(input),
      });
    }

    // 4. Execute remaining steps, checkpointing after each one. Steps
    //    already in the journal are never re-applied, so replaying a
    //    completed step cannot append its fields twice.
    const executed: string[] = [];
    for (let i = startIndex; i < plan.length; i++) {
      const step = plan[i];
      state = this.applyStep(step, state);
      executed.push(step.name);
      journal?.save({
        from,
        to,
        inputHash: hashJson(input),
        completedSteps: [...skipped, ...executed],
        state: deepClone(state),
      });
    }

    // 5. Publish only on full success; the journal is then discarded.
    options.publish?.({ version: to, data: deepClone(state) });
    journal?.clear();
    return { version: to, data: state, executedSteps: executed, skippedSteps: skipped };
  }

  private verifyCheckpoint(
    prior: Journal,
    input: JsonValue,
    plan: MigrationStep[],
    from: string,
    to: string,
  ): number {
    if (prior.from !== from || prior.to !== to) {
      throw new MigrationError(
        "CHECKPOINT_PLAN_MISMATCH",
        `journal is for "${prior.from} -> ${prior.to}", requested "${from} -> ${to}"`,
        { path: `${from} -> ${to}` },
      );
    }
    const actualHash = hashJson(input);
    if (prior.inputHash !== actualHash) {
      throw new MigrationError(
        "CHECKPOINT_INPUT_MISMATCH",
        "original input does not match the checkpoint journal",
        { path: "$" },
      );
    }
    for (let i = 0; i < prior.completedSteps.length; i++) {
      const planned = plan[i];
      if (!planned || planned.name !== prior.completedSteps[i]) {
        throw new MigrationError(
          "CHECKPOINT_PLAN_MISMATCH",
          `journal step #${i + 1} "${prior.completedSteps[i]}" is not a prefix of the planned route`,
          { path: `${from} -> ${to}`, step: prior.completedSteps[i] },
        );
      }
    }
    return prior.completedSteps.length;
  }

  private applyStep(step: MigrationStep, state: JsonValue): JsonValue {
    const inputIssues = validate(state, step.inputSchema);
    if (inputIssues.length > 0) {
      throw new MigrationError(
        "SCHEMA_INPUT",
        `input of step "${step.name}" violates its schema`,
        { step: step.name, path: inputIssues[0].path, issues: inputIssues },
      );
    }

    let output: JsonValue;
    try {
      output = step.apply(deepClone(state), { from: step.from, to: step.to });
    } catch (cause) {
      throw new MigrationError(
        "STEP_APPLY",
        `step "${step.name}" threw: ${(cause as Error).message}`,
        { step: step.name, path: "$" },
      );
    }

    const jsonIssues = checkJsonCompatible(output);
    if (jsonIssues.length > 0) {
      throw new MigrationError(
        "SCHEMA_OUTPUT",
        `output of step "${step.name}" is not a JSON tree`,
        { step: step.name, path: jsonIssues[0].path, issues: jsonIssues },
      );
    }
    const outputIssues = validate(output, step.outputSchema);
    if (outputIssues.length > 0) {
      throw new MigrationError(
        "SCHEMA_OUTPUT",
        `output of step "${step.name}" violates its schema`,
        { step: step.name, path: outputIssues[0].path, issues: outputIssues },
      );
    }
    return output;
  }
}

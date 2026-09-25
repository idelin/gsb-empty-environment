/**
 * 迁移执行引擎。
 *
 * Envelope：迁移的输入 / 输出载体，携带 version 与 data（JSON 兼容树）。
 * Checkpoint：每完成一个步骤就持久化一次，记录原始输入哈希、已执行步骤、
 *             当时数据哈希与版本。恢复时从原始输入重放（绝不会在旧结果上叠加），
 *             保证“重放已完成步骤不会重复追加字段”。
 *
 * 失败语义：任何一步失败都抛出错误，绝不发布新版本（结果 Envelope 不会产生）。
 */
import type { JsonObject, JsonValue } from './json.ts';
import { canonicalHash, deepClone, deepEqual, isObject } from './json.ts';
import { validateSchema } from './schema.ts';
import type { MigrationContext, MigrationEdge, MigrationGraph } from './graph.ts';
import { resolvePath } from './graph.ts';
import { MigrationError, fail } from './errors.ts';
import type { ValidationIssue } from './errors.ts';

export interface Envelope<V extends string = string> {
  version: V;
  data: JsonObject;
}

export interface JournalEntry {
  edgeId: string;
  stepName: string;
  /** 该步骤完成后的版本（等于所属边的 to）。 */
  version: string;
  outputHash: string;
}

export type CheckpointStatus = 'in-progress' | 'completed';

export interface Checkpoint {
  checkpointVersion: 1;
  graphHash: string;
  fromVersion: string;
  targetVersion: string;
  /** 原始输入 Envelope 的内容哈希（恢复必须校验一致）。 */
  inputHash: string;
  /** 原始输入 Envelope（恢复依据：从它重放，而不是从中间态继续）。 */
  originalEnvelope: Envelope;
  versionPath: string[];
  edgePath: string[];
  executed: JournalEntry[];
  status: CheckpointStatus;
}

export interface MigrationResult {
  envelope: Envelope;
  checkpoint: Checkpoint;
  executed: JournalEntry[];
  versionPath: string[];
}

/** 图的稳定标识：边 id 序列 + 优先级（不含 apply 函数；同一程序内定义稳定）。 */
export function graphFingerprint(graph: MigrationGraph): string {
  const descriptor: JsonValue = graph.edges.map((e) => ({
    id: e.id,
    from: e.from,
    to: e.to,
    priority: e.priority,
    steps: e.steps.map((s) => ({ name: s.name })),
  }));
  return canonicalHash(descriptor);
}

export function assertEnvelope(value: unknown, where: string): asserts value is Envelope {
  if (!isObject(value as JsonValue)) {
    fail({ code: 'INVALID_ENVELOPE', message: `${where}: envelope must be an object`, path: '' });
  }
  const env = value as Record<string, unknown>;
  if (typeof env.version !== 'string' || env.version.length === 0) {
    fail({ code: 'INVALID_ENVELOPE', message: `${where}: envelope.version must be a non-empty string`, path: '/version' });
  }
  if (!isObject(env.data as JsonValue)) {
    fail({ code: 'INVALID_ENVELOPE', message: `${where}: envelope.data must be a JSON object`, path: '/data' });
  }
}

function schemaFailure(
  code: 'INPUT_SCHEMA_VIOLATION' | 'OUTPUT_SCHEMA_VIOLATION',
  issues: ValidationIssue[],
  ctx: MigrationContext,
): never {
  const first = issues[0] as ValidationIssue;
  throw new MigrationError({
    code,
    message:
      code === 'INPUT_SCHEMA_VIOLATION'
        ? `input schema violated at edge ${ctx.edgeId} step ${ctx.stepName}`
        : `output schema violated at edge ${ctx.edgeId} step ${ctx.stepName}`,
    path: first.path,
    edgeId: ctx.edgeId,
    versionPath: ctx.versionPath,
    causeLocation: `/edges[${ctx.edgeId}]/steps[${ctx.stepName}]`,
    issues,
  });
}

export interface Plan {
  edges: MigrationEdge[];
  versionPath: string[];
  edgePath: string[];
}

export function planMigration(graph: MigrationGraph, from: string, to: string): Plan {
  const resolved = resolvePath(graph, from, to);
  return {
    edges: resolved.edges,
    versionPath: resolved.versionPath,
    edgePath: resolved.edges.map((e) => e.id),
  };
}

export function flattenSteps(plan: Plan): Array<{ edge: MigrationEdge; stepIndex: number }> {
  const flat: Array<{ edge: MigrationEdge; stepIndex: number }> = [];
  for (const edge of plan.edges) {
    edge.steps.forEach((_, stepIndex) => flat.push({ edge, stepIndex }));
  }
  return flat;
}

/** 实际执行 n 个步骤（prefix 已通过外部校验）。 */
function executePrefix(
  plan: Plan,
  original: Envelope,
  count: number,
  onStep?: (checkpoint: Checkpoint, index: number) => void,
  base?: Checkpoint,
): { envelope: Envelope; checkpoint: Checkpoint } {
  const flat = flattenSteps(plan);
  if (count > flat.length) {
    fail({
      code: 'CHECKPOINT_EXECUTION_MISMATCH',
      message: 'executed prefix is longer than the planned path',
      expected: flat.length,
      actual: count,
    });
  }

  // 关键：始终从原始输入重放，避免在旧结果上二次追加。
  let data: JsonObject = deepClone(original.data);
  let version = plan.versionPath[0] as string;
  const executed: JournalEntry[] = [];

  for (let i = 0; i < count; i++) {
    const { edge, stepIndex } = flat[i] as { edge: MigrationEdge; stepIndex: number };
    const step = edge.steps[stepIndex] as (typeof edge.steps)[number];
    const isLastInEdge = stepIndex === edge.steps.length - 1;
    const ctx: MigrationContext = {
      edgeId: edge.id,
      stepName: step.name,
      versionPath: plan.versionPath,
    };

    const inputIssues = validateSchema(data, step.input);
    if (inputIssues.length > 0) schemaFailure('INPUT_SCHEMA_VIOLATION', inputIssues, ctx);

    let output: JsonObject;
    try {
      output = step.apply(deepClone(data), ctx);
    } catch (err) {
      throw new MigrationError({
        code: 'STEP_FAILED',
        message: `step ${step.name} of edge ${edge.id} failed`,
        edgeId: edge.id,
        versionPath: plan.versionPath,
        cause: err instanceof Error ? err.message : String(err),
        causeLocation: `/edges[${edge.id}]/steps[${step.name}]`,
      });
    }

    if (!isObject(output)) {
      throw new MigrationError({
        code: 'OUTPUT_SCHEMA_VIOLATION',
        message: `step ${step.name} returned a non-object`,
        path: '',
        edgeId: edge.id,
        versionPath: plan.versionPath,
        causeLocation: `/edges[${edge.id}]/steps[${step.name}]`,
      });
    }

    const outputIssues = validateSchema(output, step.output);
    if (outputIssues.length > 0) schemaFailure('OUTPUT_SCHEMA_VIOLATION', outputIssues, ctx);

    data = output;
    if (isLastInEdge) version = edge.to;
    else version = `${edge.to}#${step.name}`;

    const entry: JournalEntry = {
      edgeId: edge.id,
      stepName: step.name,
      version: edge.to,
      outputHash: canonicalHash(output),
    };
    executed.push(entry);

    if (onStep && base) {
      const checkpoint: Checkpoint = {
        ...base,
        executed: [...executed],
        status: executed.length === flat.length ? 'completed' : 'in-progress',
      };
      onStep(checkpoint, i);
    }
  }

  // 所有步骤完成后，版本必须是目标版本。
  if (count === flat.length) version = plan.versionPath[plan.versionPath.length - 1] as string;

  const checkpoint: Checkpoint = base
    ? {
        ...base,
        executed: [...executed],
        status: count === flat.length ? 'completed' : 'in-progress',
      }
    : (undefined as unknown as Checkpoint);

  return { envelope: { version, data }, checkpoint };
}

export interface MigrateOptions {
  onCheckpoint?: (checkpoint: Checkpoint, index: number) => void;
}

/**
 * 从原始 Envelope 执行到 targetVersion。
 * 路径歧义 / schema 违规 / 步骤失败都会抛错，且不会产生结果 Envelope。
 */
export function migrate(
  graph: MigrationGraph,
  input: Envelope,
  targetVersion: string,
  options: MigrateOptions = {},
): MigrationResult {
  assertEnvelope(input, 'migrate input');
  if (input.version === targetVersion) {
    fail({
      code: 'NO_PATH',
      message: `source and target versions are identical: ${targetVersion}`,
      versionPath: [targetVersion],
    });
  }
  const plan = planMigration(graph, input.version, targetVersion);

  const rootIssues = validateSchema(input.data, (plan.edges[0] as MigrationEdge).steps[0]!.input);
  if (rootIssues.length > 0) {
    schemaFailure('INPUT_SCHEMA_VIOLATION', rootIssues, {
      edgeId: (plan.edges[0] as MigrationEdge).id,
      stepName: (plan.edges[0] as MigrationEdge).steps[0]!.name,
      versionPath: plan.versionPath,
    });
  }

  const originalEnvelope: Envelope = deepClone(input);
  const base: Checkpoint = {
    checkpointVersion: 1,
    graphHash: graphFingerprint(graph),
    fromVersion: originalEnvelope.version,
    targetVersion,
    inputHash: canonicalHash(originalEnvelope),
    originalEnvelope,
    versionPath: plan.versionPath,
    edgePath: plan.edgePath,
    executed: [],
    status: 'in-progress',
  };

  const total = flattenSteps(plan).length;
  const { envelope, checkpoint } = executePrefix(
    plan,
    originalEnvelope,
    total,
    options.onCheckpoint,
    base,
  );

  if (checkpoint.status !== 'completed') {
    fail({ code: 'STEP_FAILED', message: 'internal: migration ended without completion' });
  }
  return {
    envelope,
    checkpoint,
    executed: checkpoint.executed,
    versionPath: plan.versionPath,
  };
}

/**
 * 校验断点并从原始输入恢复。
 * 校验项：图哈希、目标版本、原始输入哈希、版本路径，以及已执行步骤的逐步骤哈希。
 */
export function resume(
  graph: MigrationGraph,
  checkpoint: Checkpoint,
  currentInput: Envelope,
  options: MigrateOptions = {},
): MigrationResult {
  assertEnvelope(currentInput, 'resume input');

  if (checkpoint.checkpointVersion !== 1) {
    fail({
      code: 'CHECKPOINT_CORRUPT',
      message: `unsupported checkpoint version ${String(checkpoint.checkpointVersion)}`,
      location: '/checkpointVersion',
      expected: 1,
      actual: checkpoint.checkpointVersion,
    });
  }

  const graphHash = graphFingerprint(graph);
  if (checkpoint.graphHash !== graphHash) {
    fail({
      code: 'CHECKPOINT_GRAPH_MISMATCH',
      message: 'checkpoint was produced by a different graph definition',
      expected: graphHash,
      actual: checkpoint.graphHash,
    });
  }

  if (currentInput.version !== checkpoint.fromVersion) {
    fail({
      code: 'CHECKPOINT_INPUT_MISMATCH',
      message: 'resume input version differs from the checkpoint original version',
      path: '/version',
      expected: checkpoint.fromVersion,
      actual: currentInput.version,
    });
  }

  const inputHash = canonicalHash(currentInput);
  if (checkpoint.inputHash !== inputHash) {
    fail({
      code: 'CHECKPOINT_INPUT_MISMATCH',
      message: 'resume input differs from the checkpoint original input',
      path: '',
      expected: checkpoint.inputHash,
      actual: inputHash,
    });
  }

  if (
    !deepEqual(checkpoint.originalEnvelope.version, currentInput.version) ||
    !deepEqual(checkpoint.originalEnvelope.data, currentInput.data)
  ) {
    fail({
      code: 'CHECKPOINT_INPUT_MISMATCH',
      message: 'checkpoint originalEnvelope does not match the provided input',
      path: '',
    });
  }

  const plan = planMigration(graph, checkpoint.fromVersion, checkpoint.targetVersion);
  if (JSON.stringify(plan.versionPath) !== JSON.stringify(checkpoint.versionPath)) {
    fail({
      code: 'CHECKPOINT_TARGET_MISMATCH',
      message: 'resolved version path differs from the checkpoint path',
      versionPath: plan.versionPath,
      candidates: [checkpoint.versionPath],
    });
  }
  if (JSON.stringify(plan.edges.map((e) => e.id)) !== JSON.stringify(checkpoint.edgePath)) {
    fail({
      code: 'CHECKPOINT_TARGET_MISMATCH',
      message: 'resolved edge path differs from the checkpoint path',
      candidates: [checkpoint.edgePath],
    });
  }

  const flat = flattenSteps(plan);
  if (checkpoint.executed.length > flat.length) {
    fail({
      code: 'CHECKPOINT_EXECUTION_MISMATCH',
      message: 'checkpoint contains more executed steps than the planned path',
      expected: flat.length,
      actual: checkpoint.executed.length,
    });
  }

  // 逐条核对已执行步骤的边 / 步骤名，并通过重放核对输出哈希。
  const verificationBase: Checkpoint = {
    ...checkpoint,
    executed: [],
    status: 'in-progress',
  };
  const { checkpoint: verified } = executePrefix(
    plan,
    currentInput,
    checkpoint.executed.length,
    undefined,
    verificationBase,
  );

  for (let i = 0; i < checkpoint.executed.length; i++) {
    const recorded = checkpoint.executed[i] as JournalEntry;
    const replay = verified.executed[i] as JournalEntry;
    if (recorded.edgeId !== replay.edgeId || recorded.stepName !== replay.stepName) {
      fail({
        code: 'CHECKPOINT_EXECUTION_MISMATCH',
        message: `executed step ${i} does not match the planned step`,
        expected: `${replay.edgeId}/${replay.stepName}`,
        actual: `${recorded.edgeId}/${recorded.stepName}`,
        edgeId: recorded.edgeId,
      });
    }
    if (recorded.outputHash !== replay.outputHash) {
      fail({
        code: 'CHECKPOINT_EXECUTION_MISMATCH',
        message: `executed step ${i} (${recorded.stepName}) produced a different output on replay`,
        expected: replay.outputHash,
        actual: recorded.outputHash,
        edgeId: recorded.edgeId,
      });
    }
  }

  if (checkpoint.status === 'completed') {
    if (checkpoint.executed.length !== flat.length) {
      fail({
        code: 'CHECKPOINT_EXECUTION_MISMATCH',
        message: 'completed checkpoint does not cover the full planned path',
        expected: flat.length,
        actual: checkpoint.executed.length,
      });
    }
    const { envelope } = executePrefix(plan, currentInput, flat.length, undefined, verificationBase);
    return {
      envelope,
      checkpoint,
      executed: checkpoint.executed,
      versionPath: plan.versionPath,
    };
  }

  // 从原始输入完整重放到终点（已完成步骤只执行一次，不重复追加字段）。
  const continueBase: Checkpoint = {
    ...checkpoint,
  };
  const { envelope, checkpoint: finalCheckpoint } = executePrefix(
    plan,
    currentInput,
    flat.length,
    options.onCheckpoint,
    continueBase,
  );
  return {
    envelope,
    checkpoint: finalCheckpoint,
    executed: finalCheckpoint.executed,
    versionPath: plan.versionPath,
  };
}

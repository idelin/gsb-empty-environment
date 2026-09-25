import { TransformError, BatchError, type SerializedError } from "./errors.ts";
import { getAtPath, type JsonValue, type Path } from "./json.ts";
import {
  emptyCheckpoint,
  MemoryCheckpointStore,
  type CheckpointData,
  type CheckpointStore,
} from "./checkpoint.ts";

/** 输入记录：id 用于去重与错误归属，seq 表示流内顺序。 */
export interface RecordEnvelope {
  id: string;
  seq: number;
  data: JsonValue;
}

export interface StepContext {
  readonly recordId: string;
  readonly stepName: string;
  /** 抛出带记录 ID、步骤名与路径的 TransformError。 */
  fail(path: Path, message: string): never;
  /** 取记录内某路径的值，便于校验后报错。 */
  at(path: Path): JsonValue | undefined;
}

export interface TransformStep {
  readonly name: string;
  apply(data: JsonValue, ctx: StepContext): JsonValue;
}

export type Mode = "atomic" | "isolated";

export type PipelineEvent =
  | { type: "record"; id: string; seq: number; data: JsonValue }
  | { type: "error"; error: TransformError }
  | { type: "checkpoint"; seq: number; emitted: number; failed: number };

export interface RunStats {
  emitted: number;
  failed: number;
  checkpoints: number;
}

export interface PipelineOptions {
  id?: string;
  steps: TransformStep[];
  mode: Mode;
  store?: CheckpointStore;
  /** 每处理多少条写一次检查点，默认 1。 */
  checkpointEvery?: number;
}

export class StreamPipeline {
  readonly id: string;
  readonly stats: RunStats = { emitted: 0, failed: 0, checkpoints: 0 };

  private readonly steps: TransformStep[];
  private readonly mode: Mode;
  private readonly store: CheckpointStore;
  private readonly checkpointEvery: number;

  constructor(options: PipelineOptions) {
    if (options.steps.length === 0) throw new Error("pipeline needs at least one step");
    this.id = options.id ?? "pipeline";
    this.steps = options.steps;
    this.mode = options.mode;
    this.store = options.store ?? new MemoryCheckpointStore();
    this.checkpointEvery = Math.max(1, options.checkpointEvery ?? 1);
  }

  /**
   * 流式处理记录。
   * - isolated：坏记录产出 error 事件，后续好记录照常产出，顺序保持。
   * - atomic：任何一条失败即抛 BatchError，此前不产出任何 record 事件。
   * 恢复运行时，已产出的记录跳过，已登记的错误不重复计数。
   */
  async *run(
    input: Iterable<RecordEnvelope> | AsyncIterable<RecordEnvelope>,
  ): AsyncGenerator<PipelineEvent, void> {
    const state = (await this.store.load(this.id)) ?? emptyCheckpoint(this.id);
    const emitted = new Set(state.emittedIds);
    const errorLedger = new Map(state.errors.map((e) => [e.recordId, e]));
    this.syncStats(emitted, errorLedger);

    if (this.mode === "atomic") {
      yield* this.runAtomic(input, state, emitted, errorLedger);
    } else {
      yield* this.runIsolated(input, state, emitted, errorLedger);
    }
  }

  private async *runAtomic(
    input: Iterable<RecordEnvelope> | AsyncIterable<RecordEnvelope>,
    state: CheckpointData,
    emitted: Set<string>,
    errorLedger: Map<string, SerializedError>,
  ): AsyncGenerator<PipelineEvent, void> {
    // 先缓冲全部结果，全部成功才发布；任一失败抛 BatchError，不产出任何记录。
    const buffered: RecordEnvelope[] = [];
    for await (const record of input) {
      if (emitted.has(record.id)) continue;
      let data = record.data;
      for (const step of this.steps) {
        try {
          data = step.apply(data, this.makeContext(record, step));
        } catch (err) {
          throw new BatchError(this.asTransformError(err, record, step));
        }
      }
      buffered.push({ ...record, data });
    }
    for (const record of buffered) {
      emitted.add(record.id);
      state.lastSeq = Math.max(state.lastSeq, record.seq);
      yield { type: "record", id: record.id, seq: record.seq, data: record.data };
    }
    if (buffered.length > 0) {
      await this.persist(state, emitted, errorLedger);
      this.syncStats(emitted, errorLedger);
      yield { type: "checkpoint", seq: state.lastSeq, emitted: emitted.size, failed: errorLedger.size };
    }
  }

  private async *runIsolated(
    input: Iterable<RecordEnvelope> | AsyncIterable<RecordEnvelope>,
    state: CheckpointData,
    emitted: Set<string>,
    errorLedger: Map<string, SerializedError>,
  ): AsyncGenerator<PipelineEvent, void> {
    let sinceCheckpoint = 0;
    for await (const record of input) {
      if (emitted.has(record.id)) continue; // 恢复时跳过已产出记录，不重复产出
      let data = record.data;
      let failure: TransformError | undefined;
      for (const step of this.steps) {
        try {
          data = step.apply(data, this.makeContext(record, step));
        } catch (err) {
          failure = this.asTransformError(err, record, step);
          break;
        }
      }
      if (failure) {
        const known = errorLedger.has(record.id);
        errorLedger.set(record.id, failure.toJSON()); // 按记录 ID 去重，恢复后不重复计数
        if (!known) yield { type: "error", error: failure };
      } else {
        errorLedger.delete(record.id); // 恢复后重试成功，从台账移除
        emitted.add(record.id);
        yield { type: "record", id: record.id, seq: record.seq, data };
      }
      state.lastSeq = Math.max(state.lastSeq, record.seq);
      sinceCheckpoint += 1;
      if (sinceCheckpoint >= this.checkpointEvery) {
        await this.persist(state, emitted, errorLedger);
        this.syncStats(emitted, errorLedger);
        sinceCheckpoint = 0;
        yield { type: "checkpoint", seq: state.lastSeq, emitted: emitted.size, failed: errorLedger.size };
      }
    }
    if (sinceCheckpoint > 0) {
      await this.persist(state, emitted, errorLedger);
      this.syncStats(emitted, errorLedger);
      yield { type: "checkpoint", seq: state.lastSeq, emitted: emitted.size, failed: errorLedger.size };
    }
  }

  private makeContext(record: RecordEnvelope, step: TransformStep): StepContext {
    return {
      recordId: record.id,
      stepName: step.name,
      fail(path, message) {
        throw new TransformError(record.id, step.name, path, message);
      },
      at(path) {
        return getAtPath(record.data, path);
      },
    };
  }

  private asTransformError(err: unknown, record: RecordEnvelope, step: TransformStep): TransformError {
    if (err instanceof TransformError) return err;
    const message = err instanceof Error ? err.message : String(err);
    return new TransformError(record.id, step.name, [], message, { cause: err });
  }

  private async persist(
    state: CheckpointData,
    emitted: Set<string>,
    errorLedger: Map<string, SerializedError>,
  ): Promise<void> {
    state.emittedIds = [...emitted];
    state.errors = [...errorLedger.values()];
    await this.store.save(state);
    this.stats.checkpoints += 1;
  }

  private syncStats(emitted: Set<string>, errorLedger: Map<string, unknown>): void {
    this.stats.emitted = emitted.size;
    this.stats.failed = errorLedger.size;
  }
}

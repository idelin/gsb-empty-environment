import { TransformError } from './errors.ts';
import type { JsonObject } from './json.ts';
import { runPipeline, type Step } from './pipeline.ts';
import type { CheckpointStore } from './checkpoint.ts';

export type RunMode = 'atomic' | 'isolated';

export interface RunStats {
  /** Records pulled from the input stream (including skipped ones). */
  seen: number;
  /** Records published by this run. */
  emitted: number;
  /** Records that failed this run (isolated mode only). */
  failed: number;
  /** Records skipped because the checkpoint already covers them. */
  skipped: number;
}

export interface RunResult {
  mode: RunMode;
  /** True when an atomic batch was committed and published by this run. */
  committed: boolean;
  /** Published records, in input order. Empty when an atomic run failed. */
  emitted: JsonObject[];
  /** Failures with record id, step, and JSON path. */
  errors: TransformError[];
  stats: RunStats;
}

export interface TransformStreamOptions {
  /** Input records, pulled lazily one at a time. */
  records: Iterable<JsonObject> | AsyncIterable<JsonObject>;
  /** Ordered pipeline steps applied to every record. */
  steps: readonly Step[];
  /**
   * `atomic`:   any failure publishes nothing; success publishes all at once.
   * `isolated`: each record stands alone; one bad record never blocks others.
   */
  mode: RunMode;
  /** Durable progress; records already in the store are never reprocessed. */
  checkpoint: CheckpointStore;
  /** Extract the record id. Defaults to the record's `id` field. */
  getId?: (record: JsonObject, index: number) => string;
  /** Streaming sink, invoked in input order as records are published. */
  onEmit?: (record: JsonObject, recordId: string) => void;
}

function defaultGetId(record: JsonObject, index: number): string {
  const id = record.id;
  if (typeof id === 'string' || typeof id === 'number') return String(id);
  throw new Error(
    `record at index ${index} has no usable "id" field; pass getId to TransformStreamOptions`,
  );
}

async function* toAsyncIterable(
  records: Iterable<JsonObject> | AsyncIterable<JsonObject>,
): AsyncGenerator<JsonObject> {
  yield* records as AsyncIterable<JsonObject>;
}

/**
 * Stream records through the pipeline.
 *
 * Guarantees:
 * - Output order always matches input order.
 * - A record already present in the checkpoint is skipped: never re-emitted,
 *   never re-counted (recovery is idempotent).
 * - `atomic`: a single failure aborts the run and publishes nothing; the
 *   commit marker in the checkpoint makes "publish" happen at most once.
 * - `isolated`: a failing record is marked `failed` and the stream continues
 *   with the next record.
 */
export async function transformStream(opts: TransformStreamOptions): Promise<RunResult> {
  const { steps, mode, checkpoint } = opts;
  const getId = opts.getId ?? defaultGetId;
  const emitted: JsonObject[] = [];
  const errors: TransformError[] = [];
  const stats: RunStats = { seen: 0, emitted: 0, failed: 0, skipped: 0 };

  const publish = (record: JsonObject, id: string): void => {
    emitted.push(record);
    stats.emitted += 1;
    opts.onEmit?.(record, id);
  };

  if (mode === 'isolated') {
    let index = 0;
    for await (const record of toAsyncIterable(opts.records)) {
      const id = getId(record, index);
      stats.seen += 1;
      if (checkpoint.get(id) !== undefined) {
        stats.skipped += 1;
        index += 1;
        continue;
      }
      try {
        const out = runPipeline(record, steps, { recordId: id, recordIndex: index });
        checkpoint.mark(id, 'emitted');
        publish(out, id);
      } catch (err) {
        const error =
          err instanceof TransformError
            ? err
            : new TransformError({
                recordId: id,
                step: '<unknown>',
                path: [],
                message: err instanceof Error ? err.message : String(err),
                cause: err,
              });
        checkpoint.mark(id, 'failed');
        stats.failed += 1;
        errors.push(error);
      }
      index += 1;
    }
    checkpoint.flush();
    return { mode, committed: true, emitted, errors, stats };
  }

  // atomic mode
  if (checkpoint.isCommitted()) {
    // Batch already published by a previous run: emit nothing again.
    for await (const _ of toAsyncIterable(opts.records)) stats.seen += 1;
    checkpoint.flush();
    return { mode, committed: false, emitted: [], errors: [], stats };
  }

  const staged: { id: string; out: JsonObject }[] = [];
  let index = 0;
  for await (const record of toAsyncIterable(opts.records)) {
    const id = getId(record, index);
    stats.seen += 1;
    const prior = checkpoint.get(id);
    if (prior === 'staged') {
      staged.push({ id, out: checkpoint.getOutput(id) ?? record });
      stats.skipped += 1;
      index += 1;
      continue;
    }
    if (prior !== undefined) {
      stats.skipped += 1;
      index += 1;
      continue;
    }
    try {
      const out = runPipeline(record, steps, { recordId: id, recordIndex: index });
      checkpoint.mark(id, 'staged', out);
      staged.push({ id, out });
    } catch (err) {
      // All-or-nothing: publish nothing, leave staged rows for resume.
      const error =
        err instanceof TransformError
          ? err
          : new TransformError({
              recordId: id,
              step: '<unknown>',
              path: [],
              message: err instanceof Error ? err.message : String(err),
              cause: err,
            });
      errors.push(error);
      checkpoint.flush();
      return { mode, committed: false, emitted: [], errors, stats };
    }
    index += 1;
  }

  checkpoint.commit();
  for (const { id, out } of staged) publish(out, id);
  checkpoint.flush();
  return { mode, committed: true, emitted, errors, stats };
}

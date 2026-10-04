/**
 * A chain of capacity-limited processing stages connected by bounded
 * channels, plus a producer at the head and a consumer (sink) at the tail.
 *
 * Backpressure: every link is a BoundedChannel, so a paused consumer fills
 * the sink channel, which blocks the last stage's send, which stops it from
 * receiving, and so on up to the producer — downstream pauses progressively
 * block upstream pulls.
 *
 * Failure & cleanup: any stage failure (or an early consumer exit) aborts
 * the whole chain. Elements already transferred are released by their
 * current owner: the receiving stage releases elements it received, and
 * elements still sitting inside a channel are drained and released by the
 * pipeline on behalf of the downstream stage.
 */

import { BoundedChannel } from "./channel.ts";
import { ElementTracker, TrackedElement } from "./elements.ts";

export interface StageContext {
  readonly signal: AbortSignal;
  /** Create a tracked output element. Outputs must be fresh elements. */
  create<T>(value: T): TrackedElement<T>;
}

export type StageFn<I, O> = (
  input: TrackedElement<I>,
  ctx: StageContext,
) => void | TrackedElement<O>[] | Promise<void | TrackedElement<O>[]>;

export interface StageDef<I = unknown, O = unknown> {
  readonly name: string;
  /** Capacity of this stage's input channel. */
  readonly capacity: number;
  readonly fn: StageFn<I, O>;
}

export interface PipelineOptions {
  readonly tracker: ElementTracker;
  /** Capacity of the channel feeding the sink. Defaults to 1. */
  readonly sinkCapacity?: number;
}

export interface RunResult {
  readonly consumerStopped: boolean;
  readonly produced: number;
  readonly consumed: number;
}

export type Sink = (
  el: TrackedElement<unknown>,
) => void | boolean | Promise<void | boolean>;

export class Pipeline {
  private readonly defs: readonly StageDef[];

  constructor(defs: readonly StageDef[]) {
    if (defs.length === 0) throw new Error("pipeline needs at least one stage");
    this.defs = defs;
  }

  async run<T>(
    source: Iterable<T> | AsyncIterable<T>,
    sink: Sink,
    options: PipelineOptions,
  ): Promise<RunResult> {
    const tracker = options.tracker;
    const controller = new AbortController();
    const signal = controller.signal;

    let failed = false;
    let failure: unknown;
    let consumerStopped = false;
    let produced = 0;
    let consumed = 0;

    const stageChannels = this.defs.map(
      (d) => new BoundedChannel<TrackedElement<unknown>>(d.capacity, `${d.name}.in`),
    );
    const sinkChannel = new BoundedChannel<TrackedElement<unknown>>(
      options.sinkCapacity ?? 1,
      "sink.in",
    );
    const allChannels = [...stageChannels, sinkChannel];

    const abort = (err?: unknown): void => {
      if (signal.aborted) return;
      if (err !== undefined) {
        failed = true;
        failure = err;
      }
      controller.abort();
      for (const ch of allChannels) ch.close(err);
    };

    const ctx: StageContext = {
      signal,
      create: <V>(value: V): TrackedElement<V> => tracker.create(value),
    };

    const producer = (async (): Promise<void> => {
      const first = stageChannels[0];
      try {
        for await (const value of source) {
          if (signal.aborted) break;
          const el = tracker.create<unknown>(value);
          produced += 1;
          try {
            await first.send(el);
          } catch {
            // Transfer never happened: the producer still owns the element.
            el.release(`producer:${first.name}`);
            break;
          }
        }
      } finally {
        first.close(failure);
      }
    })();

    const runStage = async (def: StageDef, index: number): Promise<void> => {
      const inCh = stageChannels[index];
      const outCh =
        index + 1 < stageChannels.length ? stageChannels[index + 1] : sinkChannel;
      try {
        for (;;) {
          let el: TrackedElement<unknown> | undefined;
          try {
            el = await inCh.receive();
          } catch {
            break; // input channel closed with an error
          }
          if (el === undefined) break; // input channel closed and drained
          if (signal.aborted) {
            // Received but never processed: this stage owns it.
            el.release(`abort:${def.name}`);
            break;
          }
          let outputs: TrackedElement<unknown>[];
          try {
            outputs = ((await def.fn(el, ctx)) ?? []) as TrackedElement<unknown>[];
          } catch (err) {
            el.release(def.name);
            abort(err);
            break;
          }
          // The stage consumed its input; ownership of outputs moves on send.
          el.release(def.name);
          let sendFailed = false;
          for (const out of outputs) {
            try {
              await outCh.send(out);
            } catch {
              // Downstream is gone: the send never transferred ownership.
              out.release(`${def.name}->${outCh.name}`);
              sendFailed = true;
            }
          }
          if (sendFailed || signal.aborted) break;
        }
      } finally {
        outCh.close(failure);
      }
    };

    const consumer = (async (): Promise<void> => {
      try {
        for (;;) {
          let el: TrackedElement<unknown> | undefined;
          try {
            el = await sinkChannel.receive();
          } catch {
            break;
          }
          if (el === undefined) break;
          if (signal.aborted) {
            el.release("abort:sink");
            break;
          }
          consumed += 1;
          let keepGoing: void | boolean;
          try {
            keepGoing = await sink(el);
          } catch (err) {
            el.release("sink");
            abort(err);
            break;
          }
          el.release("sink");
          if (keepGoing === false) {
            consumerStopped = true;
            abort();
            break;
          }
        }
      } finally {
        sinkChannel.close(failure);
      }
    })();

    const tasks: Promise<void>[] = [
      producer,
      consumer,
      ...this.defs.map((d, i) => runStage(d, i)),
    ];
    const settled = await Promise.allSettled(tasks);

    // Everything still inside a channel was transferred to (but never picked
    // up by) the downstream stage, so release it on that stage's behalf.
    for (let i = 0; i < stageChannels.length; i += 1) {
      for (const el of stageChannels[i].drain()) {
        el.release(`cleanup:${this.defs[i].name}`);
      }
    }
    for (const el of sinkChannel.drain()) el.release("cleanup:sink");

    if (!failed) {
      for (const r of settled) {
        if (r.status === "rejected") {
          failed = true;
          failure = r.reason;
          break;
        }
      }
    }
    if (failed) throw failure;
    return { consumerStopped, produced, consumed };
  }
}

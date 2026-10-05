import { Channel } from './channel.ts';
import { Tracker, TrackedItem } from './tracked.ts';

export interface StageSpec<T> {
  name: string;
  process: (item: TrackedItem<T>) => Promise<void> | void;
}

/**
 * A chain of processing stages connected by bounded channels.
 *
 * For N stages there are N+1 channels: ch0 feeds stage 1, chN feeds the
 * consumer. Ownership rules on any failure:
 *  - the item a stage is currently holding is released by that stage;
 *  - items already transferred into a channel buffer belong to the
 *    receiving stage, which releases them by draining its input;
 *  - an item whose `send` was interrupted still belongs to the sender.
 * Failure propagates both ways: the input channel is failed so blocked
 * upstream sends reject, and the output channel is failed so downstream
 * recvs throw.
 */
export class Pipeline<T> {
  readonly channels: Array<Channel<TrackedItem<T>>> = [];
  readonly settled: Promise<void>;
  private readonly tracker: Tracker;

  constructor(stages: Array<StageSpec<T>>, capacities: number[], tracker: Tracker) {
    if (capacities.length !== stages.length + 1) {
      throw new RangeError('capacities must have exactly stages.length + 1 entries');
    }
    this.tracker = tracker;
    for (let i = 0; i <= stages.length; i++) {
      this.channels.push(new Channel<TrackedItem<T>>(capacities[i], `ch${i}`));
    }
    const tasks = stages.map((spec, i) =>
      this.runStage(spec, this.channels[i], this.channels[i + 1]),
    );
    // Rejects only after EVERY stage has finished its cleanup, so
    // awaiting `settled` guarantees no stage still owns an item.
    this.settled = Promise.allSettled(tasks).then((results) => {
      const failure = results.find((r) => r.status === 'rejected');
      if (failure && failure.status === 'rejected') throw failure.reason;
    });
  }

  get input(): Channel<TrackedItem<T>> {
    return this.channels[0];
  }

  get output(): Channel<TrackedItem<T>> {
    return this.channels[this.channels.length - 1];
  }

  /** Consumer early exit: fail every channel so all stages unwind. */
  abort(reason: unknown): void {
    for (const channel of this.channels) channel.fail(reason);
  }

  private async runStage(
    spec: StageSpec<T>,
    input: Channel<TrackedItem<T>>,
    output: Channel<TrackedItem<T>>,
  ): Promise<void> {
    let owned: TrackedItem<T> | null = null;
    try {
      for (;;) {
        const result = await input.recv();
        if (result.done) {
          output.close();
          return;
        }
        owned = result.item as TrackedItem<T>;
        this.tracker.handoff(input.name, spec.name, owned);
        await spec.process(owned);
        this.tracker.handoff(spec.name, output.name, owned);
        await output.send(owned);
        owned = null;
      }
    } catch (err) {
      if (owned) {
        this.tracker.release(spec.name, owned);
        owned = null;
      }
      for (const item of input.drain()) {
        this.tracker.release(spec.name, item);
      }
      input.fail(err);
      output.fail(err);
      throw err;
    }
  }
}

/**
 * Pulls values from `pull`, wraps them in numbered tracked items and
 * sends them into the pipeline. Because `send` blocks on a full
 * channel, upstream pulls stop automatically when downstream pauses.
 */
export async function runProducer<T>(
  pipeline: Pipeline<T>,
  tracker: Tracker,
  pull: () => T | undefined,
): Promise<void> {
  let owned: TrackedItem<T> | null = null;
  try {
    for (;;) {
      const value = pull();
      if (value === undefined) {
        pipeline.input.close();
        return;
      }
      owned = tracker.produce(value);
      tracker.handoff('producer', pipeline.input.name, owned);
      await pipeline.input.send(owned);
      owned = null;
    }
  } catch (err) {
    if (owned) {
      tracker.release('producer', owned);
      owned = null;
    }
    throw err;
  }
}

/**
 * Receives items from the pipeline output, hands each to `onItem` and
 * releases it afterwards. On failure/abort it releases the item it
 * currently holds and drains the output channel, since items already
 * transferred to the consumer side are the consumer's responsibility.
 */
export async function runConsumer<T>(
  pipeline: Pipeline<T>,
  tracker: Tracker,
  onItem: (item: TrackedItem<T>) => Promise<void> | void,
): Promise<void> {
  const output = pipeline.output;
  let owned: TrackedItem<T> | null = null;
  try {
    for (;;) {
      const result = await output.recv();
      if (result.done) return;
      owned = result.item as TrackedItem<T>;
      tracker.handoff(output.name, 'consumer', owned);
      await onItem(owned);
      tracker.release('consumer', owned);
      owned = null;
    }
  } catch (err) {
    if (owned) {
      tracker.release('consumer', owned);
      owned = null;
    }
    for (const item of output.drain()) {
      tracker.release('consumer', item);
    }
    throw err;
  }
}

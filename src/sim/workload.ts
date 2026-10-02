import { Simulator } from './simulator.ts';
import { TraceRecorder } from './trace.ts';

export interface WorkloadResult {
  served: number;
  dropped: number;
  maxQueue: number;
  steps: number;
  checksum: string;
  trace: ReturnType<TraceRecorder['entries']['slice']>;
}

export interface WorkloadOptions {
  events: number;
  seed: number;
  capacity?: number;
  buggy?: boolean;
  storeTrace?: boolean;
}

export function runWorkload(options: WorkloadOptions): WorkloadResult {
  const sim = new Simulator(options.seed);
  const capacity = options.capacity ?? 1_000_000;
  let queue = 0;
  let served = 0;
  let dropped = 0;
  let maxQueue = 0;
  let scheduled = 0;

  const recorder = new TraceRecorder({ stateFn: () => `q=${queue},s=${served},d=${dropped}`, store: options.storeTrace ?? true });

  const scheduleArrival = (s: Simulator) => {
    if (scheduled >= options.events) return;
    scheduled++;
    if (queue >= capacity) {
      dropped++;
    } else {
      queue++;
      if (queue > maxQueue) maxQueue = queue;
      const serviceTime = 1 + s.rng.nextInt(5);
      s.schedule(serviceTime, 'service', () => {
        queue--;
        served++;
      });
    }
    const gap = options.buggy
      ? s.rng.nextInt(3)
      : 1 + s.rng.nextInt(3);
    s.schedule(gap, 'arrival', scheduleArrival);
  };

  sim.schedule(0, 'arrival', scheduleArrival);
  const result = sim.run({ observer: recorder.observer() });
  sim.dispose();

  return {
    served,
    dropped,
    maxQueue,
    steps: result.steps,
    checksum: recorder.checksum(),
    trace: recorder.entries,
  };
}

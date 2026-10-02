import { PRNG } from './prng.ts';
import { LatchManager } from './txn.ts';
import { runSimulation } from './simulation.ts';
import type { SimConfig, SimResult } from './simulation.ts';

export interface Trace {
  config: SimConfig;
  hash: string;
}

export interface ReproResult {
  ok: boolean;
  expectedHash: string;
  actualHash: string;
  result: SimResult;
}

export function recordTrace(config: SimConfig): Trace {
  const result = runSimulation(config);
  return { config, hash: result.hash };
}

export function reproduce(trace: Trace): ReproResult {
  const result = runSimulation(trace.config);
  return {
    ok: result.hash === trace.hash,
    expectedHash: trace.hash,
    actualHash: result.hash,
    result,
  };
}

export interface InterleaveResult {
  finalValue: number;
  expectedValue: number;
  bugReproduced: boolean;
  latchLeaks: number;
  steps: number;
}

export function runInterleaved(
  seed: number,
  workers: number,
  iters: number,
  useLatches: boolean,
  throwAt?: { worker: number; iter: number },
): InterleaveResult {
  const rng = new PRNG(seed);
  const latches = new LatchManager();
  let value = 0;
  let steps = 0;

  function* worker(id: number): Generator<void, void, void> {
    for (let i = 0; i < iters; i++) {
      if (useLatches) {
        while (!latches.tryAcquire(0, id)) yield;
      }
      try {
        const v = value;
        yield;
        if (throwAt && throwAt.worker === id && throwAt.iter === i) {
          throw new Error(`injected fault in worker ${id} at iter ${i}`);
        }
        value = v + 1;
      } finally {
        if (useLatches && latches.tryAcquire(0, id)) latches.release(0, id);
      }
      yield;
    }
  }

  const gens: Generator<void, void, void>[] = [];
  for (let w = 0; w < workers; w++) gens.push(worker(w + 1));
  const alive = new Set(gens);

  while (alive.size > 0) {
    const arr = [...alive];
    const g = arr[rng.int(arr.length)];
    try {
      if (g.next().done) alive.delete(g);
    } catch {
      alive.delete(g);
    }
    steps++;
  }

  const expectedValue = workers * iters;
  return {
    finalValue: value,
    expectedValue,
    bugReproduced: value !== expectedValue,
    latchLeaks: latches.outstanding,
    steps,
  };
}

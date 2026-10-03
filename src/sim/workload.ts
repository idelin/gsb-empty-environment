import { hashState } from "./hash.ts";
import { Simulation } from "./simulation.ts";

/**
 * Canonical deterministic workload: a single-server queue.
 *  - "arrival": increments the queue, schedules the next arrival and a
 *    service completion if the server is free.
 *  - "service": completes a job, starts serving the next queued job.
 * All randomness flows through the seeded PRNG, so a given seed reproduces
 * the exact same event stream forever.
 */
export interface QueueState {
  queued: number;
  served: number;
  busy: boolean;
}

export function buildQueueSimulation(seed: number, recordTrace = false): Simulation {
  const state: QueueState = { queued: 0, served: 0, busy: false };
  const sim = new Simulation({
    seed,
    recordTrace,
    contextHash: () => hashState(state),
  });

  sim.on("arrival", (s) => {
    state.queued++;
    if (!state.busy) {
      state.busy = true;
      state.queued--;
      s.schedule(1 + s.prng.nextInt(5), "service");
    }
    s.schedule(1 + s.prng.nextInt(3), "arrival");
  });

  sim.on("service", (s) => {
    state.served++;
    if (state.queued > 0) {
      state.queued--;
      s.schedule(1 + s.prng.nextInt(5), "service");
    } else {
      state.busy = false;
    }
  });

  sim.schedule(0, "arrival");
  return sim;
}

import type { Script } from './model.ts';

export const LATENCY_TICKS = 5;

export interface AssertionFailure {
  message: string;
  stepId: string;
  stepIndex: number;
}

export type ReplayResult =
  | { outcome: 'failed'; failure: AssertionFailure; stepsExecuted: number }
  | { outcome: 'passed'; stepsExecuted: number };

interface InFlightMessage {
  from: string;
  to: string;
  targetEpoch: number;
  deliverAt: number;
  seq: number;
}

/**
 * Deterministically replays a script against a simulated protocol.
 *
 * Model: every node emits a heartbeat to each peer at the start of each
 * delay step; heartbeats arrive LATENCY_TICKS later. A heartbeat carries the
 * recipient's epoch at send time. The protocol assertion is:
 *
 *   a node must never deliver a heartbeat addressed to a previous epoch
 *   (i.e. a message that was in flight across a restart of the recipient).
 *
 * The simulation stops at the first assertion violation. The script is
 * assumed to be valid (see validate.ts); replay says nothing about
 * executability.
 */
export function replay(script: Script): ReplayResult {
  const epoch = new Map<string, number>(script.nodes.map((n) => [n, 0]));
  const pending: InFlightMessage[] = [];
  let now = 0;
  let seq = 0;

  const deliverDue = (stepId: string, stepIndex: number): AssertionFailure | null => {
    pending.sort((a, b) => a.deliverAt - b.deliverAt || a.seq - b.seq);
    while (pending.length > 0 && pending[0].deliverAt <= now) {
      const msg = pending.shift() as InFlightMessage;
      const currentEpoch = epoch.get(msg.to) as number;
      if (msg.targetEpoch !== currentEpoch) {
        return {
          message:
            `stale heartbeat ${msg.from}->${msg.to} delivered across restart ` +
            `(addressed to epoch ${msg.targetEpoch}, recipient now at epoch ${currentEpoch})`,
          stepId,
          stepIndex,
        };
      }
    }
    return null;
  };

  for (let index = 0; index < script.steps.length; index += 1) {
    const step = script.steps[index];
    switch (step.type) {
      case 'delay': {
        for (const from of script.nodes) {
          for (const to of script.nodes) {
            if (from === to) continue;
            pending.push({
              from,
              to,
              targetEpoch: epoch.get(to) as number,
              deliverAt: now + LATENCY_TICKS,
              seq: seq += 1,
            });
          }
        }
        now += step.ms;
        const failure = deliverDue(step.id, index);
        if (failure) return { outcome: 'failed', failure, stepsExecuted: index + 1 };
        break;
      }
      case 'drop': {
        let remaining = step.count;
        for (let i = 0; i < pending.length && remaining > 0; ) {
          const msg = pending[i];
          if (msg.from === step.from && msg.to === step.to) {
            pending.splice(i, 1);
            remaining -= 1;
          } else {
            i += 1;
          }
        }
        break;
      }
      case 'restart': {
        epoch.set(step.node, (epoch.get(step.node) as number) + 1);
        break;
      }
    }
  }
  return { outcome: 'passed', stepsExecuted: script.steps.length };
}

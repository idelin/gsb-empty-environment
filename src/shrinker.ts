import { removeStep, type Script } from './model.ts';
import { validateScript } from './validate.ts';
import { replay } from './simulator.ts';
import { hashScript, ReplayLog, type ReplayKind } from './replay-log.ts';

export class ShrinkError extends Error {}

export interface ShrinkResult {
  script: Script;
  replayCount: number;
}

interface Candidate {
  script: Script;
  removedStepId: string | null;
}

/**
 * Replays a candidate and records a summary. A candidate only counts as a
 * successful reduction when it is BOTH executable (valid) and still triggers
 * the assertion failure. Invalid scripts are recorded with outcome
 * 'not-replayed' and are never accepted.
 */
function evaluate(
  candidate: Candidate,
  kind: ReplayKind,
  log: ReplayLog,
): { valid: boolean; failed: boolean } {
  const errors = validateScript(candidate.script);
  if (errors.length > 0) {
    log.record({
      kind,
      scriptHash: hashScript(candidate.script),
      stepCount: candidate.script.steps.length,
      removedStepId: candidate.removedStepId,
      valid: false,
      validationErrors: errors,
      outcome: 'not-replayed',
      assertion: null,
      failedAtStepId: null,
      accepted: false,
    });
    return { valid: false, failed: false };
  }
  const result = replay(candidate.script);
  const failed = result.outcome === 'failed';
  log.record({
    kind,
    scriptHash: hashScript(candidate.script),
    stepCount: candidate.script.steps.length,
    removedStepId: candidate.removedStepId,
    valid: true,
    validationErrors: [],
    outcome: result.outcome,
    assertion: result.outcome === 'failed' ? result.failure.message : null,
    failedAtStepId: result.outcome === 'failed' ? result.failure.stepId : null,
    accepted: kind === 'candidate' && failed,
  });
  return { valid: true, failed };
}

/**
 * Greedily shrinks a failing script to a 1-minimal fixed point: in the
 * returned script, every single-step deletion either produces an invalid
 * (non-executable) script or no longer triggers the assertion failure.
 */
export function shrink(original: Script, log: ReplayLog): ShrinkResult {
  const initialErrors = validateScript(original);
  if (initialErrors.length > 0) {
    throw new ShrinkError(`input script is not executable:\n  ${initialErrors.join('\n  ')}`);
  }
  const initial = evaluate({ script: original, removedStepId: null }, 'initial', log);
  if (!initial.failed) {
    throw new ShrinkError('input script does not reproduce the assertion failure');
  }

  let current = original;
  let shrinking = true;
  while (shrinking) {
    shrinking = false;
    for (const step of current.steps) {
      const candidate = { script: removeStep(current, step.id), removedStepId: step.id };
      const { valid, failed } = evaluate(candidate, 'candidate', log);
      if (valid && failed) {
        current = candidate.script;
        shrinking = true;
        break;
      }
    }
  }

  // Verification pass: prove 1-minimality of the result. Every remaining
  // single-step deletion is replayed (when executable) and must not fail.
  for (const step of current.steps) {
    const candidate = { script: removeStep(current, step.id), removedStepId: step.id };
    const { valid, failed } = evaluate(candidate, 'verify', log);
    if (valid && failed) {
      throw new ShrinkError(
        `internal error: fixed point violated, deleting "${step.id}" still fails`,
      );
    }
  }

  evaluate({ script: current, removedStepId: null }, 'final', log);
  return { script: current, replayCount: log.entries.length };
}

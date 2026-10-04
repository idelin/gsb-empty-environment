import { ChaseResult, runChase } from './chase';
import { Schema } from './model';
import { checkPreservation, PreservationResult } from './preservation';

export interface DeterminismCheck {
  ok: boolean;
  stepCount: number;
}

export interface CheckResult {
  schema: Schema;
  chase: ChaseResult;
  preservation: PreservationResult;
  determinism: DeterminismCheck;
}

function chaseFingerprint(result: ChaseResult): string {
  return JSON.stringify({
    steps: result.steps,
    finalRoots: result.finalRoots,
    witnessRow: result.witnessRow,
    witnessFirstStep: result.witnessFirstStep,
    lossless: result.lossless,
  });
}

export function runCheck(schema: Schema): CheckResult {
  const chase = runChase(schema);
  const replay = runChase(schema);
  const determinism: DeterminismCheck = {
    ok: chaseFingerprint(chase) === chaseFingerprint(replay),
    stepCount: chase.steps.length,
  };
  const preservation = checkPreservation(schema);
  return { schema, chase, preservation, determinism };
}

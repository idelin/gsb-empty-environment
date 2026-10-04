import {
  NegotiationConflict,
  NegotiationOk,
  NegotiationResult,
  PartyDeclaration,
} from './types';
import { buildModel, isFeasible, solveOptimal } from './solver';
import { analyzeConflict } from './conflict';
import { buildTranscript } from './transcript';

/**
 * Run one negotiation round between two parties.
 *
 * The common configuration is drawn from capabilities both parties
 * declare, must satisfy every merged requires/conflicts constraint and
 * include every mandatory capability, and maximizes the combined
 * priority (sum of both parties' priorities). When no configuration
 * exists, the result carries a deletion-minimal conflict core.
 *
 * Either way the result includes a transcript that binds both original
 * declarations by hash; replaying a stale declaration in a later round
 * is detected by `verifyTranscript`.
 */
export function negotiate(
  declA: PartyDeclaration,
  declB: PartyDeclaration,
): NegotiationResult {
  if (declA.party === declB.party) {
    throw new Error('both declarations use the same party id');
  }
  const model = buildModel(declA, declB);
  const common = new Set(model.common);

  if (!isFeasible(model.constraints, common)) {
    const conflict = analyzeConflict(model);
    const result: NegotiationConflict = {
      status: 'conflict',
      conflict,
      transcript: buildTranscript(declA, declB, 'conflict', null),
    };
    return result;
  }

  const config = solveOptimal(model);
  if (config === null) {
    // Unreachable: feasibility was established above.
    throw new Error('internal error: feasible model yielded no configuration');
  }
  let score = 0;
  for (const name of config) {
    score += model.weight.get(name)!;
  }
  const result: NegotiationOk = {
    status: 'ok',
    config,
    score,
    transcript: buildTranscript(declA, declB, 'ok', config),
  };
  return result;
}

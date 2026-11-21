export * from "./types.ts";
export {
  compareStableIds,
  normalizeMigrationProblem,
  type NormalizedChannel,
  type NormalizedFrame,
  type NormalizedGrant,
  type NormalizedGroup,
  type NormalizedInstance,
  type NormalizedMessage,
  type NormalizedProblem,
  type NormalizedRevoke,
} from "./model.ts";
export {
  createAuthorizationContext,
  isGrantActiveAt,
  isInstanceAuthorized,
  privateMessagesForFrame,
  protectedMessagesForFrame,
  publicMessagesForFrame,
  type AuthorizationContext,
} from "./authorization.ts";
export {
  evaluateReset,
  planMigration,
  planMigrationAuthorization,
  planReauthentication,
  solveMigration,
  type PlanOptions,
  type ResetEvaluation,
} from "./planner.ts";
export {
  acceptMigrationPlan,
  assertMigrationPlan,
  enumerateCompatibleHistories,
  verifyMigration,
  verifyMigrationPlan,
  verifyPlan,
  type VerifyOptions,
} from "./verifier.ts";

import {
  planMigration,
  planMigrationAuthorization,
  planReauthentication,
  solveMigration,
} from "./planner.ts";
import {
  acceptMigrationPlan,
  assertMigrationPlan,
  enumerateCompatibleHistories,
  verifyMigration,
  verifyMigrationPlan,
  verifyPlan,
} from "./verifier.ts";

export const migrationPlanning = {
  plan: planMigration,
  planMigrationAuthorization,
  planReauthentication,
  solveMigration,
  verify: verifyMigration,
  verifyMigrationPlan,
  verifyPlan,
  accept: acceptMigrationPlan,
  assert: assertMigrationPlan,
  enumerateCompatibleHistories,
};

export default migrationPlanning;

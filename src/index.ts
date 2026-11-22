export * from "./types.ts";
export * from "./canonical.ts";
export * from "./tcb.ts";
export * from "./events.ts";
export * from "./policy.ts";
export * from "./evaluate.ts";
export * from "./store.ts";
export * from "./engine.ts";
export * from "./scenarios.ts";

import {
  accept,
  acceptMigration,
  decide,
  decideRelease,
  evaluate,
  evaluateGeneration,
  evaluateKeyRelease,
  evaluateRelease,
  release,
} from "./evaluate.ts";
import {
  KeyReleaseAgent,
  KeyAgent,
  MigrationAcceptance,
  MigrationAcceptanceEngine,
  compareWithReference,
  referenceEvaluate,
} from "./engine.ts";
import { createEd25519Verifier, createHmacVerifier, signRecord } from "./events.ts";
import { DurableMigrationStore, runCrashScenario } from "./store.ts";
import {
  buildMigrationScenario,
  verifyPersistenceBoundaries,
} from "./scenarios.ts";
import { compareTcb, compareTcbVectors } from "./tcb.ts";

const api = {
  evaluate: evaluateRelease,
  evaluateFn: evaluate,
  evaluateRelease,
  evaluateGeneration,
  decideRelease,
  decide,
  release,
  accept,
  evaluateKeyRelease,
  acceptMigration,
  MigrationAcceptanceEngine,
  MigrationAcceptance,
  KeyAgent,
  KeyReleaseAgent,
  DurableMigrationStore,
  runCrashScenario,
  verifyPersistenceBoundaries,
  buildMigrationScenario,
  referenceEvaluate,
  compareWithReference,
  compareTcb,
  compareTcbVectors,
  createEd25519Verifier,
  createHmacVerifier,
  signRecord,
};

export default api;

export {
  analyze,
  analyzeGraph,
  analyzeProtectionGraph,
  runAnalysis,
  verify,
  violationWitnessOf,
} from "./analyze.ts";
export {
  checkProof,
  checkSafetyProof,
  checkStateClosureProof,
  verifyProof,
  verifySafetyProof,
} from "./closure.ts";
export {
  checkWitness,
  verifyViolationWitness,
  verifyWitness,
} from "./witness.ts";
export {
  buildInitialState,
  GraphError,
  normalizeGraph,
  replayPrefix,
  stateKey,
  applyEvent,
} from "./model.ts";
export { buildComponents } from "./components.ts";
export { serializeState, deserializeState } from "./serialize.ts";
export {
  PROTECTION_CLASSES,
  DEFAULT_BUDGET,
  MAX_SPEC_EVENTS,
  classRank,
  isProtectionClass,
  stricterClass,
} from "./types.ts";

export type {
  AnalysisResult,
  AnalyzeOptions,
  Budget,
  ClosureTransition,
  ComponentSafetyProof,
  EventId,
  EventSpec,
  ExplorationStats,
  HandleId,
  InitialPathSpec,
  InvalidResult,
  ObjectId,
  Operation,
  OperationGraph,
  PathId,
  PathSpec,
  ProofCheckResult,
  ProtectionClass,
  Rejection,
  SafeResult,
  SafetyProof,
  SerializedHandle,
  SerializedObject,
  SerializedState,
  StepRecord,
  UnknownResult,
  UnsafeResult,
  ViolationDetail,
  ViolationWitness,
  VolumeConfig,
  VolumeId,
  VolumeMap,
  WitnessCheckResult,
} from "./types.ts";

import { analyze } from "./analyze.ts";
import { verifySafetyProof } from "./closure.ts";
import { verifyWitness } from "./witness.ts";
import type {
  AnalysisResult,
  AnalyzeOptions,
  OperationGraph,
  ProofCheckResult,
  SafetyProof,
  ViolationWitness,
  WitnessCheckResult,
} from "./types.ts";

/**
 * Convenience facade for callers that want to keep a configuration object.
 */
export class ProtectionVerifier {
  readonly options: AnalyzeOptions;

  constructor(options: AnalyzeOptions = {}) {
    this.options = options;
  }

  analyze(graph: OperationGraph, options: AnalyzeOptions = {}): AnalysisResult {
    return analyze(graph, { ...this.options, ...options });
  }

  verifyProof(
    graph: OperationGraph,
    proof: SafetyProof,
    options: AnalyzeOptions = {},
  ): ProofCheckResult {
    return verifySafetyProof(graph, proof, { ...this.options, ...options });
  }

  verifyWitness(
    graph: OperationGraph,
    witness: ViolationWitness,
    options: AnalyzeOptions = {},
  ): WitnessCheckResult {
    return verifyWitness(graph, witness, { ...this.options, ...options });
  }
}

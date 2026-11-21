/**
 * Importable contract-verification package.
 *
 * Public surface:
 *   - model construction types and literal constructors;
 *   - declaration compilation and per-path verification;
 *   - the branded `VerifiedSummary` plus the caller-only `invoke` gate.
 */
export {
  type Atom,
  type Literal,
  pos,
  neg,
  not,
  sameLiteral,
  intersectLiterals,
} from './formula.ts';
export { AbstractState } from './state.ts';
export {
  type NodeId,
  type ReturnTag,
  type FlowNode,
  type EntryNode,
  type AssumeNode,
  type EffectNode,
  type InvalidateNode,
  type BranchNode,
  type ReturnNode,
  type ThrowNode,
  type FlowModel,
  getNode,
  successors,
} from './model.ts';
export { enumeratePaths, type FlowPath } from './paths.ts';
export {
  type CompiledContract,
  type VerificationResult,
  type VerificationSuccess,
  type VerificationFailure,
  type PathFailure,
  compileDeclaration,
  verifyContract,
} from './verify.ts';
export {
  VERIFIED_BRAND,
  type VerifiedSummary,
  isVerifiedSummary,
} from './summary.ts';
export {
  invoke,
  assertVerified,
  UnverifiedContractError,
  PreconditionViolationError,
  UnknownExitError,
} from './client.ts';

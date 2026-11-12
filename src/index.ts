/**
 * Public surface of the contract verification package.
 *
 * Note: `sealSummary` and the verification brand are deliberately not exported.
 * Callers can obtain a {@link VerifiedSummary} only by running verification,
 * and call edges accept only sealed summaries, so failed annotations stop at
 * the module that failed instead of propagating across module boundaries.
 */
export {
  domainValues,
  enumerateValuations,
  satisfyingValuations,
  type Domain,
  type DomainDecl,
  type DomainValue,
  type Valuation,
} from "./domain.ts";

export {
  and,
  compare,
  constOperand,
  field,
  not,
  or,
  tt,
  type CompareOp,
  type Operand,
  type Predicate,
} from "./predicate.ts";

export {
  applyEffectsBlock,
  readRef,
  writeRef,
  type AbstractState,
  type BlockStep,
  type BranchNode,
  type CallNode,
  type CfgNode,
  type Contract,
  type EffectsNode,
  type EntryNode,
  type ExitSpec,
  type Implementation,
  type NodeId,
  type Ref,
  type ReturnNode,
  type ThrowNode,
} from "./model.ts";

export { stepBlock, type CallShape, type Effect } from "./effects.ts";

export {
  isVerifiedSummary,
  type ExitSummary,
  type PathCounterexample,
  type VerificationFailure,
  type VerificationResult,
  type VerificationSuccess,
  type VerifiedSummary,
} from "./summary.ts";

export {
  registryStatus,
  resetRegistry,
  lookupSummary,
  isPoisoned,
} from "./registry.ts";

export { verifyImplication, type VerifyOptions } from "./verifier.ts";

export {
  annotate,
  arg,
  implement,
  labelRef,
  ref,
  requireVerified,
  ret,
  summaryOf,
  useSummary,
  verify,
  type ContractBuilder,
  type ImplBuilder,
} from "./builder.ts";

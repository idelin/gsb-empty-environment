export {
  OPAQUE_ORIGIN,
  isSameOrigin,
  parseOrigin,
  resolveUrl,
} from './src/origin.ts';

export {
  compilePolicy,
  type CompiledPolicy,
  type FencePolicy,
} from './src/policy.ts';

export {
  FenceAdjudicator,
  FenceViolationError,
  createAdjudicator,
  type DecisionReason,
  type FenceContext,
  type FenceDecision,
  type OperationKind,
} from './src/adjudicator.ts';

export {
  installFence,
  type AncestorOriginsLike,
  type FenceDocument,
  type FenceEnforcement,
  type FenceWindow,
  type InstallOptions,
} from './src/enforcer.ts';

export type { Decision } from './decision.ts';
export {
  mergeObligations,
  mergeAll,
  type Obligation,
  type QuotaObligation,
  type FieldMaskObligation,
  type OutputFormatObligation,
  type ObligationConflict,
  type MergeResult,
} from './obligations.ts';
export {
  evaluateCondition,
  type AttributeValue,
  type AttributeType,
  type AttributeDescriptor,
  type Condition,
  type ConditionResult,
} from './condition.ts';
export {
  normalizeEnvelope,
  EnvelopeError,
  type AttributeEnvelope,
  type AttributeEnvelopeV1,
  type AttributeEnvelopeV2,
} from './attributes.ts';
export {
  combine,
  denyOverrides,
  permitOverrides,
  firstApplicable,
  type CombinerName,
  type BranchOutcome,
  type CombinedOutcome,
} from './combiners.ts';
export { evaluatePolicy, type Policy, type Rule } from './policy.ts';
export {
  ModuleRegistry,
  type PolicyModule,
  type DecisionRecord,
  type ActivationResult,
  type ReplayResult,
} from './registry.ts';
export {
  ObligationExecutor,
  type ObligationHandlers,
  type ExecutionState,
  type ExecutionStatus,
} from './executor.ts';

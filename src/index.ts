export {
  OBSERVED_TYPES,
  SampleSet,
  classify,
  collectEvidence,
  escapeSegment,
  unescapeSegment,
  type EvidenceResult,
  type FieldEvidence,
  type Json,
  type NumericBound,
  type ObservedType,
} from './evidence.ts';
export {
  IMPLEMENTED_KEYWORDS,
  validate,
  type Schema,
  type ValidationError,
} from './validate.ts';
export {
  SchemaInference,
  inferReport,
  type CandidateOption,
  type DecisionPoint,
  type Dimension,
  type Distinguisher,
  type InferenceReport,
} from './infer.ts';

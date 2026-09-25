export { Pseudonymizer, REDACTED, type PseudonymizerOptions } from "./pseudonymizer.ts";
export { PseudonymizationError } from "./errors.ts";
export { canonicalize } from "./canonicalize.ts";
export { compilePattern, matches, formatPath } from "./pathPattern.ts";
export type {
  FieldRule,
  Issue,
  JsonValue,
  LogEntry,
  Logger,
  MappingEntry,
  MappingTable,
  PseudonymizeResult,
  RuleAction,
  Stats,
  UnauthorizedPolicy,
} from "./types.ts";

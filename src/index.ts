export { anonymize } from "./anonymizer.ts";
export { canonicalize } from "./canonicalize.ts";
export { formatPath, matchRule, parseRulePath } from "./paths.ts";
export { Pseudonymizer, DEFAULT_PSEUDONYM_LENGTH } from "./pseudonym.ts";
export { MappingTable } from "./mapping.ts";
export type {
  AnonymizeError,
  AnonymizeOptions,
  AnonymizeResult,
  ErrorCode,
  FieldAction,
  FieldRule,
  JsonValue,
  MappingExport,
  PathSegment,
  UnknownFieldPolicy,
} from "./types.ts";

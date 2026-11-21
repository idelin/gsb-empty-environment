export {
  Desensitizer,
  SerializationResult,
  createDesensitizer,
  maskResponse,
  serializeResponse,
  desensitize,
  restoreWithVault,
  type DesensitizeResult,
} from "./desensitizer.ts";
export {
  AuditVault,
  exportAuditMapping,
  importAuditMapping,
  deepClone,
} from "./vault.ts";
export {
  findLeaks,
  findLeaksInText,
  findLeaksInValue,
  containsLeak,
  assertNoLeak,
  buildVariantIndex,
  normalizeSensitiveInputs,
  resolveDetection,
} from "./variants.ts";
export type {
  ResolvedDetection,
  SensitiveSpan,
  VariantIndex,
} from "./variants.ts";
export { FieldResolver, normalizeSegment } from "./fields.ts";
export {
  DesensitizationError,
  DesensitizationLeakError,
  CircularReferenceError,
  AuditDecryptionError,
} from "./errors.ts";
export type {
  AuditEntry,
  AuditReason,
  DetectionOptions,
  DesensitizerOptions,
  FieldRule,
  FieldRuleEntry,
  FieldRuleObject,
  Leak,
  LevelInput,
  MaskingStrategy,
  NestedMode,
  SensitivityLevel,
  SerializeOptions,
} from "./types.ts";

export type { JsonSchema, SchemaObject, UnsupportedReason, ValueType } from "./schema.ts";
export { findUnsupported, deepEqual, stableStringify } from "./schema.ts";
export { accepts, acceptsNonEnum, explainReject, classify } from "./evaluate.ts";
export { enumerateValues, makeInside, makeOutside, makeOfType, stringOfLength } from "./values.ts";
export { checkCompat, compareContracts } from "./compare.ts";
export type { Issue, CompareResult } from "./compare.ts";

export type {
  CheckResult,
  Env,
  Field,
  FieldShape,
  Incompatibility,
  Param,
  Prim,
  Shape,
  Type,
} from "./types.js";
export { checkReplacement } from "./check.js";
export { sampleOf, sampleRecordWith } from "./sample.js";
export { renderReport, renderShape } from "./render.js";
export * as builders from "./builders.js";

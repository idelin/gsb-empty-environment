export { sanitizeDocument, sanitizeTree } from "./src/sanitizer.ts";
export type { SanitizeOptions } from "./src/sanitizer.ts";
export {
  runSanitizationPipeline,
  sanitizeOne,
} from "./src/pipeline.ts";
export type { PipelineDocument, PipelineResult } from "./src/pipeline.ts";
export { buildDocument } from "./src/tree-builder.ts";
export { serializeNodes } from "./src/serializer.ts";
export {
  isSafeUrl,
  isEventAttribute,
  isElementAllowed,
  isAttributeAllowed,
} from "./src/policy.ts";
export type {
  DocumentNode,
  ElementNode,
  Node,
  SanitizeReport,
  RemovedItem,
} from "./src/types.ts";

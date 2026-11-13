export {
  decideLinkRel,
  decideWindowOpen,
  hasNoopener,
  isBlankTarget,
  isNamedTarget,
} from "./src/noopener.ts";
export type {
  Decision,
  LinkDecisionInput,
  WindowOpenDecision,
  WindowOpenInput,
} from "./src/noopener.ts";
export {
  rewriteAnchorTags,
  rewriteDocument,
  rewriteWindowOpenCalls,
} from "./src/pipeline.ts";
export type { PipelineOptions } from "./src/pipeline.ts";

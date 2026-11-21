export {
  TVal,
  TaintRegistry,
  previewValue,
} from "./tval.ts";
export type {
  Literal,
  Val,
  ChainStep,
  OriginFact,
  LinkFact,
  TaintFact,
} from "./tval.ts";

export { TArray, THash } from "./collections.ts";

export { TaintHost, anchoringVerdict } from "./host.ts";
export type { CaptureResult, CaptureGroup, AnchoringVerdict } from "./host.ts";

export {
  SinkHost,
  TaintViolation,
  detectMode,
} from "./sinks.ts";
export type { SinkName, SinkResult, OpenResult } from "./sinks.ts";

export { InspectionService } from "./inspection.ts";
export type { TaintReport } from "./inspection.ts";

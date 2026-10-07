export {
  DEFAULT_COMPLETION_AFFINITY,
  edit,
  range,
  type BoundaryAffinity,
  type Range,
  type RangeAffinity,
  type TextEdit,
} from "./types.ts";
export { mapOffsetThroughEdit, mapRangeThroughEdit, type RangeMapping } from "./mapping.ts";
export { EditChain, VersionedDocument } from "./chain.ts";
export { TransactionalEditor, type Transaction } from "./editor.ts";
export {
  CompletionSession,
  StaleCompletionError,
  type CompletionItem,
  type CompletionResponse,
  type MappedCompletionItem,
} from "./completion.ts";

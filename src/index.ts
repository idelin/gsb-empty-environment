export type { Affinity, Anchor, Doc, DocNode, Mark } from "./model.ts";
export {
  anchor,
  docLength,
  docText,
  isLinkMark,
  makeNode,
  markKey,
  marksEqual,
  validateDoc,
} from "./model.ts";
export type { NodeMapping, NormalizeResult } from "./normalize.ts";
export { isNormalized, normalize } from "./normalize.ts";
export type { BoundaryAnchor, FlatCell, FlatDoc } from "./flat.ts";
export { equivalent, flatten, fromFlat, markSequence, renderChars } from "./flat.ts";
export type { MarkEdit } from "./marks.ts";
export { addMark, applyMarkEdits, LinkNestingError, removeMark } from "./marks.ts";
export type { EditTransaction } from "./batch.ts";
export { applyBatch } from "./batch.ts";

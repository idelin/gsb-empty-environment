export { diffLines, lineTexts, type DiffBlock, type BlockKind } from "./diff.ts";
export { LineMapping, computeFolds, type FoldRegion } from "./mapping.ts";
export { DiffSession, type Edit, type Side } from "./session.ts";
export {
  splitLines,
  isSurrogateBoundary,
  assertSafeOffset,
  type LineInfo,
  type Eol,
} from "./text.ts";

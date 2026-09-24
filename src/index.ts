export { splitLines, joinLines, eolText, type Eol, type Line } from './lines.ts';
export {
  isHighSurrogate,
  isLowSurrogate,
  splitsSurrogatePair,
  snapOffsetBackward,
  snapOffsetForward,
  clampOffset,
} from './utf16.ts';
export { diffHunks, type Hunk } from './diff.ts';
export {
  LineDiffSession,
  type Side,
  type BlockKind,
  type LineRange,
  type DiffBlock,
  type ViewItem,
  type Position,
  type AppliedEdit,
} from './session.ts';

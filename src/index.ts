export { digestText } from "./digest.ts";
export { diffPatch } from "./diff.ts";
export { TextDocument } from "./document.ts";
export { createIdGen, randomOpId, type IdGen } from "./ids.ts";
export {
  ConflictError,
  PatchApplyError,
  makePatch,
  normalizeOps,
  inputLength,
  outputLength,
  type Op,
  type Patch,
} from "./ops.ts";
export {
  applyPatch,
  composePatches,
  invertPatch,
  rebasePatch,
  transformOps,
  validateBase,
  type Side,
} from "./patch.ts";
export {
  isPairSafeRange,
  isWellFormed,
  splitsSurrogatePair,
  utf16Length,
} from "./utf16.ts";

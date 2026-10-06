export { sanitizeRaw, sanitizeWithCaret, isValidRaw } from "./sanitize.ts";
export type { SanitizeResult } from "./sanitize.ts";
export { formatDisplay } from "./format.ts";
export {
  buildPositionMap,
  rawIndexToDisplayIndex,
  displayIndexToRawIndex,
} from "./mapping.ts";
export type { PositionMap } from "./mapping.ts";
export { applyEdit, deleteBackward, deleteForward } from "./edit.ts";
export type { EditInput, EditResult } from "./edit.ts";

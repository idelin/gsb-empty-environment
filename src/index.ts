export { Rope } from "./rope";
export {
  applyCaseTable,
  detectCaseStyle,
  defaultCaseTable,
  type CaseStyle,
  type CaseTable,
} from "./casing";
export {
  findCandidates,
  replace,
  resolveConflicts,
  type Candidate,
  type ReplaceOptions,
  type ReplaceResult,
  type Rule,
} from "./replacer";
export {
  applyEdits,
  editsFromCandidates,
  invertEdits,
  type Edit,
} from "./patch";

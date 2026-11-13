export { checkRuleCondition } from './src/checker.ts';
export {
  expandMacros,
  MacroError,
  MacroCycleError,
  MacroDepthError,
  UndefinedMacroError,
  MAX_MACRO_DEPTH,
} from './src/macros.ts';
export { checkProcNameTruncation, MAX_PROC_NAME_BYTES } from './src/proc-name.ts';
export { evaluate, isFieldAvailable } from './src/tribool.ts';
export type {
  AndNode,
  CheckInput,
  CheckReport,
  CompareNode,
  ConditionNode,
  FieldAvailabilityMap,
  Finding,
  FindingCode,
  LiteralNode,
  LiteralValue,
  MacroRefNode,
  NotNode,
  Operator,
  OrNode,
} from './src/types.ts';

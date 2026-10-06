export { CtorDef, TypeDef, TypeEnv, Value, showValue, valueDepth, valueEq } from "./types";
export { Pattern, wild, varp, ctor, _, showPattern, matchPattern } from "./pattern";
export { Ctx } from "./ctx";
export { Matrix, PatternVector, useful, patternDepth, vectorDepth } from "./matrix";
export { enumerateValues, EnumResult, reachableTypes } from "./enumerate";
export {
  analyze,
  analyzeMatch,
  AnalyzeOptions,
  BranchReport,
  CrossValidation,
  Report,
  showPatternVector,
  showVector,
} from "./check";

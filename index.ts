export { parsePattern } from './src/parser.ts';
export {
  buildNfa,
  epsilonClosure,
  move,
  nfaAccepts,
  statesThatCanReachAccept,
  findConsumedPath,
  type Nfa,
  type NfaState,
} from './src/nfa.ts';
export {
  AmbiguityDetector,
  buildFailingSuffix,
  chooseDeadChar,
  detectAmbiguity,
  type AmbiguityCycle,
} from './src/ambiguity.ts';
export {
  runBacktracking,
  type RunOptions,
  type RunResult,
  type TraceEvent,
} from './src/interpreter.ts';
export { analyzePattern } from './src/analyzer.ts';
export {
  StepCapExceededError,
  PatternSyntaxError,
  type AnalysisOptions,
  type AnalysisReport,
  type AnalysisStatus,
  type AmbiguityWitness,
  type GrowthSample,
  type ParsedPattern,
  type RegexNode,
} from './src/types.ts';

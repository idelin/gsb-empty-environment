export type {
  Criterion,
  CriterionContext,
  ExplanationKind,
  ExplanationNode,
  ForfeitScore,
  Match,
  RankEntry,
  RankingOptions,
  RankingResult,
  ResolvedOptions,
  ScoreMap,
  ScoringRules,
  StandingRow,
  TeamId,
} from './types.js';
export { computeStandings, resolveMatchScore } from './standings.js';
export type { MatchScore } from './standings.js';
export {
  defaultCriteria,
  headToHeadGoalDifference,
  headToHeadGoalsFor,
  headToHeadPoints,
  overallGoalDifference,
  overallGoalsFor,
} from './criteria.js';
export { formatExplanation, rankTeams, resolveOptions } from './ranking.js';

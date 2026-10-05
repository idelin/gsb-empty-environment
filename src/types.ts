export type TeamId = string;

export interface Match {
  home: TeamId;
  away: TeamId;
  homeScore: number;
  awayScore: number;
  forfeitedBy?: TeamId;
}

export interface StandingRow {
  team: TeamId;
  played: number;
  won: number;
  drawn: number;
  lost: number;
  goalsFor: number;
  goalsAgainst: number;
  goalDifference: number;
  points: number;
  forfeits: number;
}

export interface ScoringRules {
  win: number;
  draw: number;
  loss: number;
  forfeitLoss: number;
}

export interface ForfeitScore {
  scored: number;
  conceded: number;
}

export type ScoreMap = Map<TeamId, number>;

export interface CriterionContext {
  subgroup: TeamId[];
  allTeams: TeamId[];
  matches: Match[];
  options: ResolvedOptions;
}

export interface Criterion {
  id: string;
  label: string;
  score(ctx: CriterionContext): ScoreMap;
}

export interface RankingOptions {
  scoring?: Partial<ScoringRules>;
  forfeitScore?: ForfeitScore;
  criteria?: Criterion[];
}

export interface ResolvedOptions {
  scoring: ScoringRules;
  forfeitScore: ForfeitScore;
  criteria: Criterion[];
}

export type ExplanationKind = 'points' | 'criterion' | 'tie' | 'team';

export interface ExplanationNode {
  kind: ExplanationKind;
  teams: TeamId[];
  summary: string;
  children: ExplanationNode[];
  points?: number;
  criterionId?: string;
  criterionLabel?: string;
  tiedCriteria?: string[];
  scores?: Record<TeamId, number>;
}

export interface RankEntry {
  rank: number;
  teams: TeamId[];
  tied: boolean;
}

export interface RankingResult {
  entries: RankEntry[];
  standings: StandingRow[];
  explanation: ExplanationNode;
}

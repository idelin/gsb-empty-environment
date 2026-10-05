import type { Criterion, CriterionContext, ResolvedOptions, ScoreMap, TeamId } from './types.js';
import { resolveMatchScore } from './standings.js';

interface MiniStats {
  points: number;
  goalsFor: number;
  goalsAgainst: number;
}

type StatsMap = Map<TeamId, MiniStats>;

function matchPoints(scored: number, conceded: number, forfeited: boolean, options: ResolvedOptions): number {
  if (forfeited) return options.scoring.forfeitLoss;
  if (scored > conceded) return options.scoring.win;
  if (scored < conceded) return options.scoring.loss;
  return options.scoring.draw;
}

function accumulate(stats: StatsMap, team: TeamId, scored: number, conceded: number, points: number): void {
  const row = stats.get(team);
  if (!row) return;
  row.points += points;
  row.goalsFor += scored;
  row.goalsAgainst += conceded;
}

function collectStats(ctx: CriterionContext, subgroupOnly: boolean): StatsMap {
  const stats: StatsMap = new Map();
  for (const team of ctx.subgroup) {
    stats.set(team, { points: 0, goalsFor: 0, goalsAgainst: 0 });
  }
  const inSubgroup = new Set(ctx.subgroup);
  const inAll = new Set(ctx.allTeams);
  for (const match of ctx.matches) {
    if (!inAll.has(match.home) || !inAll.has(match.away)) continue;
    if (subgroupOnly && !(inSubgroup.has(match.home) && inSubgroup.has(match.away))) continue;
    if (!inSubgroup.has(match.home) && !inSubgroup.has(match.away)) continue;
    const score = resolveMatchScore(match, ctx.options.forfeitScore);
    accumulate(
      stats,
      match.home,
      score.homeScore,
      score.awayScore,
      matchPoints(score.homeScore, score.awayScore, match.forfeitedBy === match.home, ctx.options),
    );
    accumulate(
      stats,
      match.away,
      score.awayScore,
      score.homeScore,
      matchPoints(score.awayScore, score.homeScore, match.forfeitedBy === match.away, ctx.options),
    );
  }
  return stats;
}

function pickScores(stats: StatsMap, field: string): ScoreMap {
  const out: ScoreMap = new Map();
  for (const [team, row] of stats) {
    let value = row.goalsFor;
    if (field === 'points') value = row.points;
    else if (field === 'goalDifference') value = row.goalsFor - row.goalsAgainst;
    out.set(team, value);
  }
  return out;
}

export const headToHeadPoints: Criterion = {
  id: 'head-to-head-points',
  label: '相互战绩积分',
  score(ctx: CriterionContext): ScoreMap {
    return pickScores(collectStats(ctx, true), 'points');
  },
};

export const headToHeadGoalDifference: Criterion = {
  id: 'head-to-head-goal-difference',
  label: '相互战绩净胜球',
  score(ctx: CriterionContext): ScoreMap {
    return pickScores(collectStats(ctx, true), 'goalDifference');
  },
};

export const headToHeadGoalsFor: Criterion = {
  id: 'head-to-head-goals-for',
  label: '相互战绩进球数',
  score(ctx: CriterionContext): ScoreMap {
    return pickScores(collectStats(ctx, true), 'goalsFor');
  },
};

export const overallGoalDifference: Criterion = {
  id: 'overall-goal-difference',
  label: '总净胜球',
  score(ctx: CriterionContext): ScoreMap {
    return pickScores(collectStats(ctx, false), 'goalDifference');
  },
};

export const overallGoalsFor: Criterion = {
  id: 'overall-goals-for',
  label: '总进球数',
  score(ctx: CriterionContext): ScoreMap {
    return pickScores(collectStats(ctx, false), 'goalsFor');
  },
};

export const defaultCriteria: Criterion[] = [
  headToHeadPoints,
  headToHeadGoalDifference,
  headToHeadGoalsFor,
  overallGoalDifference,
  overallGoalsFor,
];

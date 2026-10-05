import type { MatchResult, Scoring } from './types.ts';

/**
 * 计算一组队伍在指定比赛集合中的积分。
 * 只统计双方都在 teams 集合内的比赛，因此同一个函数既可用于
 * 全体队伍的总积分，也可用于某个同分子组的“相互战绩”积分。
 */
export function computePoints(
  teams: readonly string[],
  matches: readonly MatchResult[],
  scoring: Scoring,
): Map<string, number> {
  const points = new Map<string, number>();
  for (const team of teams) points.set(team, 0);

  const add = (team: string, delta: number) => {
    points.set(team, (points.get(team) ?? 0) + delta);
  };

  for (const match of matches) {
    if (!points.has(match.home) || !points.has(match.away)) continue;
    if (match.type === 'forfeit') {
      const winner = match.forfeitedBy === 'home' ? match.away : match.home;
      add(winner, scoring.forfeitWin);
      add(match.forfeitedBy === 'home' ? match.home : match.away, scoring.forfeitLoss);
    } else if (match.homeScore === match.awayScore) {
      add(match.home, scoring.draw);
      add(match.away, scoring.draw);
    } else {
      const winner = match.homeScore > match.awayScore ? match.home : match.away;
      const loser = match.homeScore > match.awayScore ? match.away : match.home;
      add(winner, scoring.win);
      add(loser, scoring.loss);
    }
  }
  return points;
}

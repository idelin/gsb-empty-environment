import type { ForfeitScore, Match, ResolvedOptions, StandingRow, TeamId } from './types.js';

export interface MatchScore {
  homeScore: number;
  awayScore: number;
}

export function resolveMatchScore(match: Match, forfeitScore: ForfeitScore): MatchScore {
  if (match.forfeitedBy === match.home) {
    return { homeScore: forfeitScore.conceded, awayScore: forfeitScore.scored };
  }
  if (match.forfeitedBy === match.away) {
    return { homeScore: forfeitScore.scored, awayScore: forfeitScore.conceded };
  }
  return { homeScore: match.homeScore, awayScore: match.awayScore };
}

type RowMap = Map<TeamId, StandingRow>;

export function computeStandings(teams: TeamId[], matches: Match[], options: ResolvedOptions): StandingRow[] {
  const rows: RowMap = new Map();
  for (const team of teams) {
    rows.set(team, {
      team,
      played: 0,
      won: 0,
      drawn: 0,
      lost: 0,
      goalsFor: 0,
      goalsAgainst: 0,
      goalDifference: 0,
      points: 0,
      forfeits: 0,
    });
  }
  const inTable = new Set(teams);
  for (const match of matches) {
    if (!inTable.has(match.home) || !inTable.has(match.away)) continue;
    const home = rows.get(match.home);
    const away = rows.get(match.away);
    if (!home || !away) continue;
    const score = resolveMatchScore(match, options.forfeitScore);
    home.played += 1;
    away.played += 1;
    home.goalsFor += score.homeScore;
    home.goalsAgainst += score.awayScore;
    away.goalsFor += score.awayScore;
    away.goalsAgainst += score.homeScore;
    if (match.forfeitedBy === match.home) {
      home.lost += 1;
      home.forfeits += 1;
      home.points += options.scoring.forfeitLoss;
      away.won += 1;
      away.points += options.scoring.win;
    } else if (match.forfeitedBy === match.away) {
      away.lost += 1;
      away.forfeits += 1;
      away.points += options.scoring.forfeitLoss;
      home.won += 1;
      home.points += options.scoring.win;
    } else if (score.homeScore > score.awayScore) {
      home.won += 1;
      home.points += options.scoring.win;
      away.lost += 1;
      away.points += options.scoring.loss;
    } else if (score.homeScore < score.awayScore) {
      away.won += 1;
      away.points += options.scoring.win;
      home.lost += 1;
      home.points += options.scoring.loss;
    } else {
      home.drawn += 1;
      away.drawn += 1;
      home.points += options.scoring.draw;
      away.points += options.scoring.draw;
    }
  }
  const result: StandingRow[] = [];
  for (const row of rows.values()) {
    row.goalDifference = row.goalsFor - row.goalsAgainst;
    result.push(row);
  }
  return result;
}

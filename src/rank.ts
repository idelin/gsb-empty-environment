import { computePoints } from './points.ts';
import { DEFAULT_SCORING } from './types.ts';
import type {
  ExplanationNode,
  MatchResult,
  RankingResult,
  Scoring,
  Standing,
} from './types.ts';

/**
 * 赛事排名。
 *
 * 不能用一次性的排序比较器实现：相互战绩关系可能不成传递
 * （A 胜 B、B 胜 C、C 胜 A），而且拆出部分名次后，剩余子组必须
 * 只用子组内部的比赛重新决胜。这里用递归分组实现：
 *
 * 1. 按当前范围的积分（顶层为总积分，子组为组内相互战绩积分）分桶；
 * 2. 独占一个积分档的队伍直接确定名次；
 * 3. 同分且只占原组一部分的子组，递归进入下一轮相互战绩决胜；
 * 4. 同分且覆盖整个当前组（无法再拆分）时，保留并列。
 */
export function rankTeams(
  teams: readonly string[],
  matches: readonly MatchResult[],
  scoring: Partial<Scoring> = {},
): RankingResult {
  validate(teams, matches);
  const fullScoring: Scoring = { ...DEFAULT_SCORING, ...scoring };
  const overallPoints = computePoints(teams, matches, fullScoring);

  const root: ExplanationNode = {
    kind: 'points',
    summary: '',
    teams: [...teams],
    children: [],
  };
  const standings: Standing[] = [];
  resolveGroup([...teams], matches, fullScoring, overallPoints, 1, root, standings, 0);
  return { standings, explanation: root };
}

function validate(teams: readonly string[], matches: readonly MatchResult[]): void {
  const seen = new Set<string>();
  for (const team of teams) {
    if (seen.has(team)) throw new Error(`队伍重复：${team}`);
    seen.add(team);
  }
  for (const match of matches) {
    for (const side of [match.home, match.away]) {
      if (!seen.has(side)) throw new Error(`比赛出现未登记的队伍：${side}`);
    }
    if (match.home === match.away) throw new Error(`比赛双方相同：${match.home}`);
  }
}

function resolveGroup(
  group: string[],
  matches: readonly MatchResult[],
  scoring: Scoring,
  overallPoints: Map<string, number>,
  startRank: number,
  node: ExplanationNode,
  standings: Standing[],
  depth: number,
): number {
  const points = computePoints(group, matches, scoring);
  const record: Record<string, number> = {};
  for (const team of group) record[team] = points.get(team) ?? 0;
  node.points = record;

  const buckets = new Map<number, string[]>();
  for (const team of group) {
    const value = points.get(team) ?? 0;
    const bucket = buckets.get(value);
    if (bucket) bucket.push(team);
    else buckets.set(value, [team]);
  }
  const sorted = [...buckets.entries()].sort((a, b) => b[0] - a[0]);

  const scope = depth === 0 ? '总积分' : '相互战绩积分（仅计本组内比赛）';
  node.summary = `比较${scope}：${sorted
    .map(([value, names]) => `${names.join('、')}=${value}`)
    .join('，')}`;

  let rank = startRank;
  for (const [value, bucket] of sorted) {
    if (bucket.length === 1) {
      const team = bucket[0] as string;
      standings.push({ rank, teams: [team], points: overallPoints.get(team) ?? 0 });
      node.children.push({
        kind: 'resolved',
        summary: `${team} 积 ${value} 分，确定第 ${rank} 名`,
        teams: [team],
        rank,
        children: [],
      });
      rank += 1;
    } else if (bucket.length === group.length && depth > 0) {
      const tied = [...bucket].sort();
      const first = tied[0] as string;
      standings.push({ rank, teams: tied, points: overallPoints.get(first) ?? 0 });
      node.children.push({
        kind: 'tied',
        summary: `${tied.join('、')} 相互战绩仍为 ${value} 分，无法继续拆分，并列第 ${rank} 名`,
        teams: tied,
        rank,
        children: [],
      });
      rank += bucket.length;
    } else {
      const tied = [...bucket].sort();
      const child: ExplanationNode = {
        kind: 'head-to-head',
        summary: '',
        teams: tied,
        children: [],
      };
      node.children.push(child);
      rank = resolveGroup(tied, matches, scoring, overallPoints, rank, child, standings, depth + 1);
    }
  }
  return rank;
}

import type {
  ExplanationNode,
  Match,
  RankEntry,
  RankingOptions,
  RankingResult,
  ResolvedOptions,
  ScoringRules,
  TeamId,
} from './types.js';
import { defaultCriteria } from './criteria.js';
import { computeStandings } from './standings.js';

const DEFAULT_SCORING: ScoringRules = { win: 3, draw: 1, loss: 0, forfeitLoss: 0 };

export function resolveOptions(options: RankingOptions = {}): ResolvedOptions {
  const scoring: ScoringRules = {
    win: options.scoring?.win ?? DEFAULT_SCORING.win,
    draw: options.scoring?.draw ?? DEFAULT_SCORING.draw,
    loss: options.scoring?.loss ?? DEFAULT_SCORING.loss,
    forfeitLoss: options.scoring?.forfeitLoss ?? DEFAULT_SCORING.forfeitLoss,
  };
  return {
    scoring,
    forfeitScore: options.forfeitScore ?? { scored: 3, conceded: 0 },
    criteria: options.criteria ?? defaultCriteria,
  };
}

interface RankCtx {
  allTeams: TeamId[];
  matches: Match[];
  options: ResolvedOptions;
}

type TeamBuckets = Map<number, TeamId[]>;

function subgroupNode(group: TeamId[], ctx: RankCtx): ExplanationNode {
  if (group.length === 1) {
    return {
      kind: 'team',
      teams: group.slice(),
      summary: `${group[0]} 获得确定名次`,
      children: [],
    };
  }
  const tiedIds: string[] = [];
  const tiedLabels: string[] = [];
  for (const criterion of ctx.options.criteria) {
    const scores = criterion.score({
      subgroup: group,
      allTeams: ctx.allTeams,
      matches: ctx.matches,
      options: ctx.options,
    });
    const first = scores.get(group[0]) ?? 0;
    let allEqual = true;
    for (const team of group) {
      if ((scores.get(team) ?? 0) !== first) {
        allEqual = false;
        break;
      }
    }
    if (allEqual) {
      tiedIds.push(criterion.id);
      tiedLabels.push(criterion.label);
      continue;
    }
    const buckets: TeamBuckets = new Map();
    for (const team of group) {
      const value = scores.get(team) ?? 0;
      const bucket = buckets.get(value);
      if (bucket) bucket.push(team);
      else buckets.set(value, [team]);
    }
    const values = Array.from(buckets.keys()).sort((a, b) => b - a);
    const children: ExplanationNode[] = [];
    for (const value of values) {
      children.push(subgroupNode(buckets.get(value) ?? [], ctx));
    }
    const scoresRecord: Record<TeamId, number> = {};
    const parts: string[] = [];
    for (const team of group) {
      const value = scores.get(team) ?? 0;
      scoresRecord[team] = value;
      parts.push(`${team}=${value}`);
    }
    const prefix = tiedLabels.length > 0 ? `${tiedLabels.join('、')}均相同；` : '';
    return {
      kind: 'criterion',
      teams: group.slice(),
      criterionId: criterion.id,
      criterionLabel: criterion.label,
      tiedCriteria: tiedIds.slice(),
      scores: scoresRecord,
      summary: `${prefix}按「${criterion.label}」分组：${parts.join('，')}`,
      children,
    };
  }
  return {
    kind: 'tie',
    teams: group.slice(),
    tiedCriteria: tiedIds.slice(),
    summary: `所有比较条件（${tiedLabels.join('、')}）用尽仍相同，保留并列：${group.join('、')}`,
    children: [],
  };
}

function collectLeaves(node: ExplanationNode, out: ExplanationNode[]): void {
  if (node.kind === 'team' || node.kind === 'tie') {
    out.push(node);
    return;
  }
  for (const child of node.children) collectLeaves(child, out);
}

export function rankTeams(teams: TeamId[], matches: Match[], options: RankingOptions = {}): RankingResult {
  const resolved = resolveOptions(options);
  const standings = computeStandings(teams, matches, resolved);
  const ctx: RankCtx = { allTeams: teams.slice(), matches, options: resolved };

  const pointsOf: Record<TeamId, number> = {};
  for (const row of standings) pointsOf[row.team] = row.points;

  const buckets: TeamBuckets = new Map();
  for (const team of teams) {
    const points = pointsOf[team] ?? 0;
    const bucket = buckets.get(points);
    if (bucket) bucket.push(team);
    else buckets.set(points, [team]);
  }
  const values = Array.from(buckets.keys()).sort((a, b) => b - a);

  const children: ExplanationNode[] = [];
  const summaryParts: string[] = [];
  for (const value of values) {
    const group = buckets.get(value) ?? [];
    const node = subgroupNode(group, ctx);
    node.points = value;
    children.push(node);
    summaryParts.push(`${value}分[${group.join('、')}]`);
  }
  const root: ExplanationNode = {
    kind: 'points',
    teams: teams.slice(),
    summary: `按总积分分组：${summaryParts.join('；')}`,
    children,
  };

  const leaves: ExplanationNode[] = [];
  for (const child of children) collectLeaves(child, leaves);

  const entries: RankEntry[] = [];
  let placed = 0;
  for (const leaf of leaves) {
    entries.push({ rank: placed + 1, teams: leaf.teams.slice(), tied: leaf.teams.length > 1 });
    placed += leaf.teams.length;
  }

  const order: Record<TeamId, number> = {};
  entries.forEach((entry, index) => {
    for (const team of entry.teams) order[team] = index;
  });
  const sortedStandings = standings.slice().sort((a, b) => (order[a.team] ?? 0) - (order[b.team] ?? 0));

  return { entries, standings: sortedStandings, explanation: root };
}

export function formatExplanation(node: ExplanationNode, indent: string = ''): string {
  const lines: string[] = [`${indent}${node.summary}`];
  for (const child of node.children) {
    lines.push(formatExplanation(child, `${indent}  `));
  }
  return lines.join('\n');
}

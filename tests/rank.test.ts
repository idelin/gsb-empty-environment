import { test } from 'node:test';
import assert from 'node:assert/strict';
import { rankTeams, computePoints, formatExplanation, DEFAULT_SCORING } from '../src/index.ts';
import type { ExplanationNode, MatchResult } from '../src/index.ts';

const score = (home: string, away: string, homeScore: number, awayScore: number): MatchResult => ({
  type: 'score',
  home,
  away,
  homeScore,
  awayScore,
});

/** 简写：winner 击败 loser */
const win = (winner: string, loser: string): MatchResult => score(winner, loser, 1, 0);

/** 简写：loser 弃权，winner 不战而胜 */
const forfeit = (winner: string, loser: string): MatchResult => ({
  type: 'forfeit',
  home: loser,
  away: winner,
  forfeitedBy: 'home',
});

function findChild(node: ExplanationNode, kind: ExplanationNode['kind'], teams: string[]) {
  return node.children.find(
    (c) => c.kind === kind && [...c.teams].sort().join(',') === [...teams].sort().join(','),
  );
}

test('基本排名：积分各不相同，直接按总积分排名', () => {
  const matches = [
    win('W', 'X'), win('W', 'Y'), win('W', 'Z'),
    win('X', 'Y'), win('X', 'Z'),
    win('Y', 'Z'),
  ];
  const { standings, explanation } = rankTeams(['W', 'X', 'Y', 'Z'], matches);
  assert.deepEqual(
    standings.map((s) => [s.rank, s.teams, s.points]),
    [[1, ['W'], 6], [2, ['X'], 5], [3, ['Y'], 4], [4, ['Z'], 3]],
  );
  assert.equal(explanation.kind, 'points');
  assert.ok(explanation.children.every((c) => c.kind === 'resolved'));
});

test('三队循环胜负：相互战绩不构成传递关系，最终并列第一', () => {
  // A 胜 B、B 胜 C、C 胜 A —— 任何一次性比较器都会给出依赖输入顺序的错误结果
  const matches = [win('A', 'B'), win('B', 'C'), win('C', 'A')];
  const { standings, explanation } = rankTeams(['A', 'B', 'C'], matches);

  assert.equal(standings.length, 1);
  assert.equal(standings[0]!.rank, 1);
  assert.deepEqual(standings[0]!.teams, ['A', 'B', 'C']);
  assert.equal(standings[0]!.points, 3);

  const h2h = findChild(explanation, 'head-to-head', ['A', 'B', 'C']);
  assert.ok(h2h, '应先尝试相互战绩决胜');
  assert.deepEqual(h2h.points, { A: 3, B: 3, C: 3 });
  const tied = findChild(h2h, 'tied', ['A', 'B', 'C']);
  assert.ok(tied, '相互战绩仍相同，应保留并列');
  assert.equal(tied.rank, 1);
});

test('三队循环胜负：结果与比赛输入顺序无关', () => {
  const base = [win('A', 'B'), win('B', 'C'), win('C', 'A')];
  const expected = rankTeams(['A', 'B', 'C'], base).standings;
  const reversed = rankTeams(['C', 'B', 'A'], [...base].reverse()).standings;
  assert.deepEqual(reversed, expected);
});

test('弃权：弃权方 0 分，不战而胜计入积分', () => {
  const matches = [
    forfeit('D', 'E'),   // E 弃权，D 不战而胜
    win('F', 'D'),
    win('E', 'F'),
  ];
  const points = computePoints(['D', 'E', 'F'], matches, DEFAULT_SCORING);
  assert.equal(points.get('D'), 3); // 弃权胜 2 + 负 1
  assert.equal(points.get('E'), 2); // 弃权 0 + 胜 2
  assert.equal(points.get('F'), 3); // 胜 2 + 负 1

  const { standings } = rankTeams(['D', 'E', 'F'], matches);
  // D、F 同积 3 分，相互战绩 F 胜 D
  assert.deepEqual(
    standings.map((s) => [s.rank, s.teams]),
    [[1, ['F']], [2, ['D']], [3, ['E']]],
  );
});

test('弃权：相互战绩决胜时，弃权胜同样有效', () => {
  const matches = [
    forfeit('P', 'Q'), // Q 弃权，P 不战而胜
    win('P', 'S'),
    win('R', 'P'),
    win('Q', 'R'),
    win('Q', 'S'),
    win('Q', 'T'),
    win('T', 'P'),
  ];
  const { standings, explanation } = rankTeams(['P', 'Q', 'R', 'S', 'T'], matches);
  // P、Q 总积分同为 6；相互战绩为 P 因 Q 弃权获胜
  assert.deepEqual(
    standings.map((s) => [s.rank, s.teams]),
    [[1, ['P']], [2, ['Q']], [3, ['R', 'T']], [5, ['S']]],
  );
  const h2h = findChild(explanation, 'head-to-head', ['P', 'Q']);
  assert.ok(h2h);
  assert.deepEqual(h2h.points, { P: 2, Q: 0 });
});

test('多层拆组：先拆出 A、B，剩余子组重新决胜后仍并列', () => {
  // A/B/C/D 组内双循环：A 全胜，B 仅负于 A，C 与 D 各胜一场
  const matches = [
    win('A', 'B'), win('A', 'B'),
    win('A', 'C'), win('A', 'C'),
    win('A', 'D'), win('A', 'D'),
    win('B', 'C'), win('B', 'C'),
    win('B', 'D'), win('B', 'D'),
    win('C', 'D'), win('D', 'C'),
    // 平衡总积分用的组外比赛，使 A/B/C/D 总积分同为 12
    win('B', 'F'),
    win('C', 'F'), win('C', 'G'), win('H', 'C'),
    win('D', 'F'), win('D', 'G'), win('H', 'D'),
  ];
  const { standings, explanation } = rankTeams(['A', 'B', 'C', 'D', 'F', 'G', 'H'], matches);

  assert.deepEqual(
    standings.map((s) => [s.rank, s.teams, s.points]),
    [
      [1, ['A'], 12],
      [2, ['B'], 12],
      [3, ['C', 'D'], 12], // 拆组后仍同分，并列第三
      [5, ['H'], 4],
      [6, ['F'], 3],
      [7, ['G'], 2],
    ],
  );

  const level1 = findChild(explanation, 'head-to-head', ['A', 'B', 'C', 'D']);
  assert.ok(level1, '总积分相同，进入相互战绩决胜');
  assert.deepEqual(level1.points, { A: 12, B: 10, C: 7, D: 7 });
  assert.ok(findChild(level1, 'resolved', ['A']));
  assert.ok(findChild(level1, 'resolved', ['B']));

  const level2 = findChild(level1, 'head-to-head', ['C', 'D']);
  assert.ok(level2, '拆出 A、B 后，C/D 子组应重新决胜');
  assert.deepEqual(level2.points, { C: 3, D: 3 });
  const tied = findChild(level2, 'tied', ['C', 'D']);
  assert.ok(tied);
  assert.equal(tied.rank, 3);
});

test('多层拆组：三层递归后完全决出名次', () => {
  // 相互战绩：第 1 层 A 垫底、B/C/D 同分；第 2 层 B 居首、C/D 同分；第 3 层 C 胜 D
  const matches = [
    win('A', 'B'),
    win('C', 'A'), win('C', 'A'),
    win('D', 'A'), win('D', 'A'),
    win('B', 'C'),
    win('B', 'D'), win('B', 'D'),
    win('C', 'D'),
    // 平衡总积分，使 A/B/C/D 同为 7 分
    win('F', 'A'),
  ];
  const { standings, explanation } = rankTeams(['A', 'B', 'C', 'D', 'F'], matches);

  assert.deepEqual(
    standings.map((s) => [s.rank, s.teams, s.points]),
    [
      [1, ['B'], 7],
      [2, ['C'], 7],
      [3, ['D'], 7],
      [4, ['A'], 7],
      [5, ['F'], 2],
    ],
  );

  const level1 = findChild(explanation, 'head-to-head', ['A', 'B', 'C', 'D']);
  assert.ok(level1);
  assert.deepEqual(level1!.points, { A: 6, B: 7, C: 7, D: 7 });

  const level2 = findChild(level1!, 'head-to-head', ['B', 'C', 'D']);
  assert.ok(level2, '拆出 A 后应对 B/C/D 重新决胜');
  assert.deepEqual(level2!.points, { B: 6, C: 3, D: 3 });

  const level3 = findChild(level2!, 'head-to-head', ['C', 'D']);
  assert.ok(level3, '拆出 B 后应对 C/D 重新决胜');
  assert.deepEqual(level3!.points, { C: 2, D: 1 });
  assert.ok(findChild(level3!, 'resolved', ['C']));
  assert.ok(findChild(level3!, 'resolved', ['D']));
});

test('自定义积分规则：足球 3/1/0，平局后相互战绩仍同分则并列', () => {
  const matches = [
    score('M', 'N', 1, 1), // 平局
    win('M', 'O'),
    win('N', 'O'),
  ];
  const { standings } = rankTeams(['M', 'N', 'O'], matches, { win: 3, draw: 1, loss: 0 });
  assert.deepEqual(
    standings.map((s) => [s.rank, s.teams, s.points]),
    [[1, ['M', 'N'], 4], [3, ['O'], 0]],
  );
});

test('解释树可渲染为文本', () => {
  const matches = [win('A', 'B'), win('B', 'C'), win('C', 'A')];
  const { explanation } = rankTeams(['A', 'B', 'C'], matches);
  const text = formatExplanation(explanation);
  assert.match(text, /总积分/);
  assert.match(text, /相互战绩/);
  assert.match(text, /并列第 1 名/);
});

test('输入校验：未登记队伍与重复队伍', () => {
  assert.throws(() => rankTeams(['A', 'A'], []), /重复/);
  assert.throws(() => rankTeams(['A', 'B'], [win('A', 'C')]), /未登记/);
  assert.throws(() => rankTeams(['A', 'B'], [win('A', 'A')]), /相同/);
});

test('边界：单支队伍直接排名第一', () => {
  const { standings } = rankTeams(['solo'], []);
  assert.deepEqual(standings, [{ rank: 1, teams: ['solo'], points: 0 }]);
});

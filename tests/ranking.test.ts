import { formatExplanation, rankTeams, resolveMatchScore } from '../src/index.js';
import type { Match } from '../src/index.js';
import { assert, test } from './testkit.js';

test('积分不同直接按积分排名', () => {
  const result = rankTeams(['A', 'B'], [{ home: 'A', away: 'B', homeScore: 2, awayScore: 0 }]);
  assert.deepEqual(result.entries, [
    { rank: 1, teams: ['A'], tied: false },
    { rank: 2, teams: ['B'], tied: false },
  ]);
});

test('三队循环胜负且数据完全相同，保留并列', () => {
  const matches: Match[] = [
    { home: 'A', away: 'B', homeScore: 2, awayScore: 1 },
    { home: 'B', away: 'C', homeScore: 2, awayScore: 1 },
    { home: 'C', away: 'A', homeScore: 2, awayScore: 1 },
  ];
  const result = rankTeams(['A', 'B', 'C'], matches);
  assert.deepEqual(result.entries, [{ rank: 1, teams: ['A', 'B', 'C'], tied: true }]);
  const tie = result.explanation.children[0];
  assert.equal(tie.kind, 'tie');
  assert.deepEqual(tie.tiedCriteria, [
    'head-to-head-points',
    'head-to-head-goal-difference',
    'head-to-head-goals-for',
    'overall-goal-difference',
    'overall-goals-for',
  ]);
});

test('三队循环胜负，相互净胜球分出名次', () => {
  const matches: Match[] = [
    { home: 'A', away: 'B', homeScore: 3, awayScore: 0 },
    { home: 'B', away: 'C', homeScore: 1, awayScore: 0 },
    { home: 'C', away: 'A', homeScore: 1, awayScore: 0 },
  ];
  const result = rankTeams(['A', 'B', 'C'], matches);
  assert.deepEqual(result.entries.map((e) => e.teams), [['A'], ['C'], ['B']]);
  const node = result.explanation.children[0];
  assert.equal(node.kind, 'criterion');
  assert.equal(node.criterionId, 'head-to-head-goal-difference');
  assert.deepEqual(node.tiedCriteria, ['head-to-head-points']);
});

test('弃权按判负处理并计入相互战绩', () => {
  const matches: Match[] = [
    { home: 'A', away: 'B', homeScore: 2, awayScore: 1 },
    { home: 'B', away: 'C', homeScore: 0, awayScore: 0, forfeitedBy: 'C' },
    { home: 'C', away: 'A', homeScore: 1, awayScore: 0 },
  ];
  const result = rankTeams(['A', 'B', 'C'], matches);
  // 三队同积 3 分；相互净胜球 B=+2、A=0、C=-2
  assert.deepEqual(result.entries.map((e) => e.teams), [['B'], ['A'], ['C']]);
  const cRow = result.standings.find((row) => row.team === 'C');
  assert.ok(cRow);
  assert.equal(cRow.forfeits, 1);
  assert.equal(cRow.goalsFor, 1);
  assert.equal(cRow.goalsAgainst, 3);
  const bRow = result.standings.find((row) => row.team === 'B');
  assert.ok(bRow);
  assert.equal(bRow.goalsFor, 4);
});

test('弃权比分按配置生效', () => {
  const match: Match = { home: 'B', away: 'C', homeScore: 0, awayScore: 0, forfeitedBy: 'C' };
  assert.deepEqual(resolveMatchScore(match, { scored: 3, conceded: 0 }), {
    homeScore: 3,
    awayScore: 0,
  });
  const result = rankTeams(['A', 'B'], [{ home: 'A', away: 'B', homeScore: 1, awayScore: 1, forfeitedBy: 'B' }], {
    forfeitScore: { scored: 5, conceded: 0 },
  });
  const aRow = result.standings.find((row) => row.team === 'A');
  assert.ok(aRow);
  assert.equal(aRow.goalsFor, 5);
  assert.equal(aRow.points, 3);
});

test('多层拆组：先拆出部分名次，再对剩余子组重新决胜', () => {
  const matches: Match[] = [
    { home: 'A', away: 'B', homeScore: 1, awayScore: 0 },
    { home: 'B', away: 'C', homeScore: 1, awayScore: 0 },
    { home: 'C', away: 'D', homeScore: 1, awayScore: 0 },
    { home: 'D', away: 'A', homeScore: 2, awayScore: 0 },
    { home: 'A', away: 'C', homeScore: 0, awayScore: 0 },
    { home: 'B', away: 'D', homeScore: 0, awayScore: 0 },
  ];
  const result = rankTeams(['A', 'B', 'C', 'D'], matches);
  // 四队同积 4 分；相互净胜球 D=+1、B=0、C=0、A=-1，拆出 D 与 A 后
  // 对剩余子组 {B, C} 重新从相互战绩积分决胜：B 胜 C
  assert.deepEqual(result.entries.map((e) => e.teams), [['D'], ['B'], ['C'], ['A']]);
  assert.deepEqual(
    result.entries.map((e) => e.rank),
    [1, 2, 3, 4],
  );

  const group = result.explanation.children[0];
  assert.equal(group.kind, 'criterion');
  assert.equal(group.criterionId, 'head-to-head-goal-difference');
  assert.deepEqual(group.tiedCriteria, ['head-to-head-points']);
  assert.equal(group.children.length, 3);
  assert.deepEqual(group.children[0].teams, ['D']);
  assert.deepEqual(group.children[2].teams, ['A']);
  const sub = group.children[1];
  assert.equal(sub.kind, 'criterion');
  assert.equal(sub.criterionId, 'head-to-head-points');
  assert.deepEqual(
    sub.children.map((n) => n.teams),
    [['B'], ['C']],
  );
});

test('并列占用同一名次，后续名次跳号', () => {
  const matches: Match[] = [
    { home: 'P', away: 'Q', homeScore: 1, awayScore: 1 },
    { home: 'P', away: 'R', homeScore: 1, awayScore: 0 },
    { home: 'P', away: 'S', homeScore: 1, awayScore: 0 },
    { home: 'P', away: 'T', homeScore: 1, awayScore: 0 },
    { home: 'Q', away: 'R', homeScore: 1, awayScore: 0 },
    { home: 'Q', away: 'S', homeScore: 1, awayScore: 0 },
    { home: 'Q', away: 'T', homeScore: 1, awayScore: 0 },
    { home: 'R', away: 'S', homeScore: 1, awayScore: 0 },
    { home: 'S', away: 'T', homeScore: 1, awayScore: 0 },
    { home: 'T', away: 'R', homeScore: 1, awayScore: 0 },
  ];
  const result = rankTeams(['P', 'Q', 'R', 'S', 'T'], matches);
  assert.deepEqual(result.entries, [
    { rank: 1, teams: ['P', 'Q'], tied: true },
    { rank: 3, teams: ['R', 'S', 'T'], tied: true },
  ]);
});

test('解释树可格式化为可读文本', () => {
  const matches: Match[] = [
    { home: 'A', away: 'B', homeScore: 3, awayScore: 0 },
    { home: 'B', away: 'C', homeScore: 1, awayScore: 0 },
    { home: 'C', away: 'A', homeScore: 1, awayScore: 0 },
  ];
  const result = rankTeams(['A', 'B', 'C'], matches);
  const text = formatExplanation(result.explanation);
  assert.ok(text.includes('总积分'));
  assert.ok(text.includes('相互战绩净胜球'));
  assert.ok(text.includes('A 获得确定名次'));
});

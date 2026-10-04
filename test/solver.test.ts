import { test } from 'node:test';
import assert from 'node:assert/strict';
import { solve, validateAssignment, computeLoads } from '../src/index.ts';
import type { Avoidance, Judge, Work } from '../src/index.ts';

const j = (id: string, ...specialties: string[]): Judge => ({ id, specialties });
const w = (id: string, specialty = 'X'): Work => ({ id, specialty });

/** 统计分配中每对评委的共评次数 */
function pairCounts(assignment: Record<string, readonly string[]>): Map<string, number> {
  const counts = new Map<string, number>();
  for (const team of Object.values(assignment)) {
    for (let x = 0; x < team.length; x++) {
      for (let y = x + 1; y < team.length; y++) {
        const key = [team[x]!, team[y]!].sort().join('|');
        counts.set(key, (counts.get(key) ?? 0) + 1);
      }
    }
  }
  return counts;
}

test('完全均衡实例：4 评委 4 作品，最优最大差为 0', () => {
  const judges = [j('A', 'X'), j('B', 'X'), j('C', 'X'), j('D', 'X')];
  const works = [w('W1'), w('W2'), w('W3'), w('W4')];
  const r = solve(judges, works);
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  assert.equal(r.spread, 0);
  assert.deepEqual(Object.values(r.loads), [3, 3, 3, 3]);
  assert.deepEqual(validateAssignment(r.assignment, judges, works), []);
  // 该实例中每对评委恰好共评 2 次，达到联合限制上限
  for (const count of pairCounts(r.assignment).values()) assert.equal(count, 2);
});

test('评委对联合限制不可忽略：4 评委 5 作品在 ≤2 下无解（容量见证）', () => {
  const judges = [j('A', 'X'), j('B', 'X'), j('C', 'X'), j('D', 'X')];
  const works = [w('W1'), w('W2'), w('W3'), w('W4'), w('W5')];
  const r = solve(judges, works);
  assert.equal(r.status, 'infeasible');
  if (r.status !== 'infeasible') return;
  assert.equal(r.witness.kind, 'capacity');
  if (r.witness.kind !== 'capacity') return;
  // 需求 5*C(3,2)=15，容量 2*C(4,2)=12
  assert.equal(r.witness.demand, 15);
  assert.equal(r.witness.capacity, 12);
  assert.equal(r.witness.judgeIds.length, 4);
  assert.equal(r.witness.workIds.length, 5);
});

test('放宽联合限制后同一实例可行：证明限制被全局建模而非装饰', () => {
  const judges = [j('A', 'X'), j('B', 'X'), j('C', 'X'), j('D', 'X')];
  const works = [w('W1'), w('W2'), w('W3'), w('W4'), w('W5')];
  const r = solve(judges, works, [], { maxPairShared: 3 });
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  assert.deepEqual(validateAssignment(r.assignment, judges, works, [], { maxPairShared: 3 }), []);
  for (const count of pairCounts(r.assignment).values()) assert.ok(count <= 3);
});

test('回避见证：作品可用评委不足 3 人', () => {
  const judges = [j('A', 'X'), j('B', 'X'), j('C', 'X'), j('D', 'X'), j('E', 'X')];
  const works = [w('W1'), w('W2')];
  const avoidances: Avoidance[] = [
    { judgeId: 'A', workId: 'W2' },
    { judgeId: 'B', workId: 'W2' },
    { judgeId: 'C', workId: 'W2' },
  ];
  const r = solve(judges, works, avoidances);
  assert.equal(r.status, 'infeasible');
  if (r.status !== 'infeasible') return;
  assert.equal(r.witness.kind, 'avoidance');
  if (r.witness.kind !== 'avoidance') return;
  assert.equal(r.witness.workId, 'W2');
  assert.equal(r.witness.eligibleJudgeIds.length, 2);
  assert.equal(r.witness.required, 3);
});

test('规模限制：超过 12 份作品返回 error', () => {
  const judges = [j('A', 'X'), j('B', 'X'), j('C', 'X')];
  const works = Array.from({ length: 13 }, (_, i) => w(`W${i}`));
  const r = solve(judges, works);
  assert.equal(r.status, 'error');
  if (r.status === 'error') assert.match(r.message, /规模超限/);
});

test('专业匹配与回避在解中被满足', () => {
  const judges = [
    j('J1', '计算机'), j('J2', '计算机'), j('J3', '计算机'), j('J8', '计算机'),
    j('J4', '材料'), j('J5', '材料'), j('J6', '材料'), j('J7', '材料'),
  ];
  const works = [
    w('W1', '计算机'), w('W2', '计算机'),
    w('W3', '材料'), w('W4', '材料'), w('W5', '材料'), w('W6', '材料'),
  ];
  const avoidances: Avoidance[] = [{ judgeId: 'J1', workId: 'W1' }];
  const r = solve(judges, works, avoidances);
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  assert.deepEqual(validateAssignment(r.assignment, judges, works, avoidances), []);
  assert.ok(!r.assignment['W1']!.includes('J1'));
  // 材料侧 4 作品 × 3 人 = 12 恰等于 2*C(4,2)，每位材料评委被迫恰好评 3 份，
  // 计算机侧 6 人份无法让 4 位评委都 ≥2，故最优最大差为 2
  assert.equal(r.spread, 2);
  for (const id of ['J4', 'J5', 'J6', 'J7']) assert.equal(r.loads[id], 3);
});

test('最小化最大差：5 评委 4 作品最优为 1（12 人份无法被 5 整除）', () => {
  const judges = [j('A', 'X'), j('B', 'X'), j('C', 'X'), j('D', 'X'), j('E', 'X')];
  const works = [w('W1'), w('W2'), w('W3'), w('W4')];
  const r = solve(judges, works);
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  assert.equal(r.spread, 1);
  assert.equal(r.minLoad, 2);
  assert.equal(r.maxLoad, 3);
  assert.deepEqual(validateAssignment(r.assignment, judges, works), []);
});

test('空作品集合：平凡最优，最大差为 0', () => {
  const r = solve([j('A', 'X')], []);
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  assert.equal(r.spread, 0);
  assert.deepEqual(r.assignment, {});
});

test('validateAssignment 能检出违规（含评委对联合限制）', () => {
  const judges = [j('A', 'X'), j('B', 'X'), j('C', 'X')];
  const works = [w('W1'), w('W2'), w('W3')];
  const bad = { W1: ['A', 'B', 'C'], W2: ['A', 'B', 'C'], W3: ['A', 'B', 'C'] };
  const violations = validateAssignment(bad, judges, works);
  assert.ok(violations.some((v) => v.includes('联合限制')));
});

test('computeLoads 统计工作量', () => {
  const judges = [j('A', 'X'), j('B', 'X'), j('C', 'X'), j('D', 'X')];
  const loads = computeLoads({ W1: ['A', 'B', 'C'], W2: ['A', 'B', 'D'] }, judges);
  assert.deepEqual(loads, { A: 2, B: 2, C: 1, D: 1 });
});

test('12 份作品的上限规模可求解', () => {
  const judges = [
    j('J1', 'X'), j('J2', 'X'), j('J3', 'X'), j('J4', 'X'),
    j('J5', 'X'), j('J6', 'X'), j('J7', 'X'), j('J8', 'X'),
  ];
  const works = Array.from({ length: 12 }, (_, i) => w(`W${i}`));
  const r = solve(judges, works);
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  assert.deepEqual(validateAssignment(r.assignment, judges, works), []);
  // 36 人份 / 8 评委 = 4.5，最优最大差至少为 1
  assert.ok(r.spread >= 1);
});

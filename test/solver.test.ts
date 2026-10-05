import {
  InfeasibleResult,
  Judge,
  Problem,
  SolveResult,
  Work,
  solve,
  verifyAssignment,
} from '../src';

let failures = 0;

function assert(cond: boolean, msg: string): void {
  if (!cond) throw new Error(msg);
}

function assertEq<T>(actual: T, expected: T, msg: string): void {
  if (actual !== expected) throw new Error(`${msg}：期望 ${expected}，实际 ${actual}`);
}

function test(name: string, fn: () => void): void {
  try {
    fn();
    console.log(`ok - ${name}`);
  } catch (e) {
    failures++;
    console.error(`FAIL - ${name}: ${(e as Error).message}`);
  }
}

const mkWorks = (n: number, field = 'f'): Work[] =>
  Array.from({ length: n }, (_, i) => ({ id: `w${i + 1}`, field }));
const mkJudges = (n: number, fields: string[] = ['f']): Judge[] =>
  Array.from({ length: n }, (_, i) => ({ id: `j${i + 1}`, fields }));

function asInfeasible(r: SolveResult): InfeasibleResult {
  assert(r.status === 'infeasible', `期望 infeasible，实际 ${r.status}`);
  return r as InfeasibleResult;
}

// 1. 规模限制：超过 12 份作品直接拒绝
test('规模限制：13 份作品返回 unsupported', () => {
  const r = solve({ works: mkWorks(13), judges: mkJudges(9) });
  assertEq(r.status, 'unsupported', 'status');
});

// 2. 回避见证：合格评委不足 3 人
test('回避见证：回避导致合格评委不足', () => {
  const r = asInfeasible(
    solve({
      works: mkWorks(1),
      judges: mkJudges(3),
      conflicts: [{ judgeId: 'j1', workId: 'w1' }],
    }),
  );
  assert(r.witness.type === 'work-conflict', `期望 work-conflict，实际 ${r.witness.type}`);
  if (r.witness.type === 'work-conflict') {
    assertEq(r.witness.workId, 'w1', '见证作品');
    assertEq(r.witness.eligibleJudgeIds.length, 2, '合格评委数');
    assert(r.witness.blockedByConflict.includes('j1'), '应列出被回避排除的 j1');
  }
});

// 3. 容量见证：3 名评委评 12 份作品，评委对联合限制下最多 6 个席位
test('容量见证：3 评委 12 作品违反评委对联合限制', () => {
  const r = asInfeasible(solve({ works: mkWorks(12), judges: mkJudges(3) }));
  assert(r.witness.type === 'capacity', `期望 capacity，实际 ${r.witness.type}`);
  if (r.witness.type === 'capacity') {
    // 按子集大小升序找到的是极小见证：3 份作品需要 9 个席位，上界仅 6
    assertEq(r.witness.maxSlots, 6, '容量上界');
    assertEq(r.witness.requiredSlots, 9, '所需席位');
  }
});

// 4. 容量见证：6 名评委评 12 份作品，每人最多 5 份，共 30 < 36
test('容量见证：6 评委 12 作品席位不足', () => {
  const r = asInfeasible(solve({ works: mkWorks(12), judges: mkJudges(6) }));
  assert(r.witness.type === 'capacity', `期望 capacity，实际 ${r.witness.type}`);
  if (r.witness.type === 'capacity') assertEq(r.witness.maxSlots, 30, '容量上界');
});

// 5. 最优差 0：9 评委 12 作品，平均 4 份（仿射平面解存在）
test('最优解：9 评委 12 作品工作量最大差为 0', () => {
  const r = solve({ works: mkWorks(12), judges: mkJudges(9) });
  assert(r.status === 'ok', `期望 ok，实际 ${r.status}`);
  if (r.status !== 'ok') return;
  assertEq(r.maxLoadDiff, 0, '工作量最大差');
  const v = verifyAssignment({ works: mkWorks(12), judges: mkJudges(9) }, r.assignment);
  assert(v.valid, `校验失败：${v.violations.join('；')}`);
});

// 6. 最优差 1：7 评委 12 作品，总量 36 不可均分，差至少为 1
test('最优解：7 评委 12 作品工作量最大差为 1', () => {
  const p: Problem = { works: mkWorks(12), judges: mkJudges(7) };
  const r = solve(p);
  assert(r.status === 'ok', `期望 ok，实际 ${r.status}`);
  if (r.status !== 'ok') return;
  assertEq(r.maxLoadDiff, 1, '工作量最大差');
  assert(verifyAssignment(p, r.assignment).valid, '校验失败');
});

// 7. 最优差 0：6 评委 6 作品
test('最优解：6 评委 6 作品工作量最大差为 0', () => {
  const p: Problem = { works: mkWorks(6), judges: mkJudges(6) };
  const r = solve(p);
  assert(r.status === 'ok', `期望 ok，实际 ${r.status}`);
  if (r.status !== 'ok') return;
  assertEq(r.maxLoadDiff, 0, '工作量最大差');
  assert(verifyAssignment(p, r.assignment).valid, '校验失败');
});

// 8. 最优差 1：5 评委 6 作品，平均 3.6 不可均分
test('最优解：5 评委 6 作品工作量最大差为 1', () => {
  const p: Problem = { works: mkWorks(6), judges: mkJudges(5) };
  const r = solve(p);
  assert(r.status === 'ok', `期望 ok，实际 ${r.status}`);
  if (r.status !== 'ok') return;
  assertEq(r.maxLoadDiff, 1, '工作量最大差');
  assert(verifyAssignment(p, r.assignment).valid, '校验失败');
});

// 8b. 组合核心：评委对上限 P=1 时，5 评委 3 作品容量必要条件均满足
//     （评委席位上界 10 >= 9，评委对容量 10 >= 9），但 3 个三元组需要
//     9 个互不相同的评委对，5 元集合上不存在（Steiner 三元系要求 n ≡ 1,3 mod 6）
test('组合核心：P=1 时 5 评委 3 作品精确不可行', () => {
  const p: Problem = { works: mkWorks(3), judges: mkJudges(5), maxPairCoJudging: 1 };
  const r = asInfeasible(solve(p));
  assert(
    r.witness.type === 'combinatorial-core',
    `期望 combinatorial-core，实际 ${r.witness.type}`,
  );
  if (r.witness.type !== 'combinatorial-core') return;
  const coreIds = r.witness.workIds;
  const coreWorks = p.works.filter((w) => coreIds.includes(w.id));
  const sub: Problem = { works: coreWorks, judges: p.judges, maxPairCoJudging: 1 };
  assertEq(solve(sub).status, 'infeasible', '核心子问题应不可行');
  for (const w of coreWorks) {
    const reduced: Problem = { works: coreWorks.filter((x) => x.id !== w.id), judges: p.judges, maxPairCoJudging: 1 };
    assertEq(solve(reduced).status, 'ok', `删去 ${w.id} 后应可行`);
  }
});

// 9. 专业 + 回避：分专业评委，带回避关系，结果须通过独立校验
test('专业与回避：分专业场景求解并通过校验', () => {
  const judges: Judge[] = [
    ...Array.from({ length: 4 }, (_, i) => ({ id: `a${i + 1}`, fields: ['A'] })),
    ...Array.from({ length: 4 }, (_, i) => ({ id: `b${i + 1}`, fields: ['B'] })),
  ];
  const works: Work[] = [
    ...Array.from({ length: 4 }, (_, i) => ({ id: `wa${i + 1}`, field: 'A' })),
    ...Array.from({ length: 4 }, (_, i) => ({ id: `wb${i + 1}`, field: 'B' })),
  ];
  const p: Problem = {
    works,
    judges,
    conflicts: [
      { judgeId: 'a1', workId: 'wa1' },
      { judgeId: 'b2', workId: 'wb3' },
    ],
  };
  const r = solve(p);
  assert(r.status === 'ok', `期望 ok，实际 ${r.status === 'infeasible' ? JSON.stringify(r.witness) : r.status}`);
  if (r.status !== 'ok') return;
  const v = verifyAssignment(p, r.assignment);
  assert(v.valid, `校验失败：${v.violations.join('；')}`);
  assert(!r.assignment['wa1'].includes('a1'), 'wa1 不应分配给回避评委 a1');
  assert(!r.assignment['wb3'].includes('b2'), 'wb3 不应分配给回避评委 b2');
  for (const [wid, js] of Object.entries(r.assignment)) {
    const prefix = wid.startsWith('wa') ? 'a' : 'b';
    for (const j of js) assert(j.startsWith(prefix), `${wid} 应分配给 ${prefix} 专业评委`);
  }
});

// 10. 评委对联合限制确实生效：结果中任意评委对共评不超过 2 份
test('联合限制：结果中所有评委对共评数 <= 2', () => {
  const p: Problem = { works: mkWorks(12), judges: mkJudges(8) };
  const r = solve(p);
  assert(r.status === 'ok', `期望 ok，实际 ${r.status}`);
  if (r.status !== 'ok') return;
  for (const [key, c] of Object.entries(r.pairCoJudging)) {
    assert(c <= 2, `评委对 ${key} 共评 ${c} 份，违反联合限制`);
  }
});

// 11. verifyAssignment 能发现违规分配
test('校验器：能识别违反评委对联合限制的分配', () => {
  const p: Problem = { works: mkWorks(3), judges: mkJudges(3) };
  const bad: Record<string, string[]> = { w1: ['j1', 'j2', 'j3'], w2: ['j1', 'j2', 'j3'], w3: ['j1', 'j2', 'j3'] };
  const v = verifyAssignment(p, bad);
  assert(!v.valid, '应判定无效');
  assert(v.violations.some((s) => s.includes('评委对')), '应报告评委对违规');
});

// 12. 性能冒烟：12 作品 12 评委多专业带回避
test('性能冒烟：12 作品 12 评委多专业场景可快速求解', () => {
  const fields = ['X', 'Y', 'Z'];
  const works: Work[] = Array.from({ length: 12 }, (_, i) => ({ id: `w${i + 1}`, field: fields[i % 3] }));
  const judges: Judge[] = Array.from({ length: 12 }, (_, i) => ({
    id: `j${i + 1}`,
    fields: [fields[i % 3], fields[(i + 1) % 3]],
  }));
  const conflicts = [
    { judgeId: 'j1', workId: 'w4' },
    { judgeId: 'j2', workId: 'w7' },
    { judgeId: 'j5', workId: 'w1' },
    { judgeId: 'j8', workId: 'w10' },
  ];
  const p: Problem = { works, judges, conflicts };
  const r = solve(p);
  assert(r.status === 'ok', `期望 ok，实际 ${r.status === 'infeasible' ? JSON.stringify(r.witness) : r.status}`);
  if (r.status !== 'ok') return;
  assert(verifyAssignment(p, r.assignment).valid, '校验失败');
});

if (failures > 0) {
  throw new Error(`${failures} 个测试失败`);
}
console.log('全部测试通过');

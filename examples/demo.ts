import { solve, validateAssignment } from '../src/index.ts';
import type { Avoidance, Judge, Work } from '../src/index.ts';

function printResult(label: string, result: ReturnType<typeof solve>): void {
  console.log(`\n=== ${label} ===`);
  if (result.status === 'optimal') {
    console.log(`最优解：工作量最大差 = ${result.spread}（负载 ${result.minLoad}..${result.maxLoad}）`);
    for (const [work, team] of Object.entries(result.assignment)) {
      console.log(`  ${work} -> ${team.join(', ')}`);
    }
    console.log('  各评委工作量:', JSON.stringify(result.loads));
    console.log(`  搜索统计: ${result.stats.nodes} 节点 / ${result.stats.attempts} 次界尝试 / ${result.stats.elapsedMs}ms`);
  } else if (result.status === 'infeasible') {
    const w = result.witness;
    if (w.kind === 'avoidance') {
      console.log(`无解（回避见证）：作品 ${w.workId} 可用评委仅 ${w.eligibleJudgeIds.length} 人（需 ${w.required} 人）: [${w.eligibleJudgeIds.join(', ')}]`);
    } else if (w.kind === 'capacity') {
      console.log(`无解（容量见证）：作品子集 {${w.workIds.join(', ')}} 只能由评委池 {${w.judgeIds.join(', ')}} 评审`);
      console.log(`  评委对名额需求 ${w.demand} > 联合限制下容量 ${w.capacity}`);
    } else {
      console.log(`无解（穷举证明）：${w.detail}`);
    }
  } else {
    console.log(`错误：${result.message}`);
  }
}

// 场景 1：正常求解 —— 3 个专业方向、9 位评委、8 份作品、若干回避关系
const judges: Judge[] = [
  { id: 'J1', specialties: ['计算机'] },
  { id: 'J2', specialties: ['计算机'] },
  { id: 'J3', specialties: ['计算机', '材料'] },
  { id: 'J4', specialties: ['材料'] },
  { id: 'J5', specialties: ['材料'] },
  { id: 'J6', specialties: ['生物'] },
  { id: 'J7', specialties: ['生物'] },
  { id: 'J8', specialties: ['生物', '计算机'] },
  { id: 'J9', specialties: ['材料', '生物'] },
];
const works: Work[] = [
  { id: 'W1', specialty: '计算机' },
  { id: 'W2', specialty: '计算机' },
  { id: 'W3', specialty: '计算机' },
  { id: 'W4', specialty: '材料' },
  { id: 'W5', specialty: '材料' },
  { id: 'W6', specialty: '材料' },
  { id: 'W7', specialty: '生物' },
  { id: 'W8', specialty: '生物' },
];
const avoidances: Avoidance[] = [
  { judgeId: 'J1', workId: 'W1' }, // 同单位回避
  { judgeId: 'J4', workId: 'W5' },
  { judgeId: 'J6', workId: 'W8' },
];

const r1 = solve(judges, works, avoidances);
printResult('场景 1：8 份作品 / 9 位评委', r1);
if (r1.status === 'optimal') {
  const violations = validateAssignment(r1.assignment, judges, works, avoidances);
  console.log(`  约束校验：${violations.length === 0 ? '全部通过' : violations.join('；')}`);
}

// 场景 2：回避见证 —— W9 被回避到只剩 2 位可用评委
const r2 = solve(judges, [...works, { id: 'W9', specialty: '计算机' }], [
  ...avoidances,
  { judgeId: 'J1', workId: 'W9' },
  { judgeId: 'J2', workId: 'W9' },
  { judgeId: 'J8', workId: 'W9' },
]);
printResult('场景 2：回避见证', r2);

// 场景 3：容量见证 —— 5 份作品只能由 4 位评委评审，
// 评委对名额需求 5*C(3,2)=15 > 2*C(4,2)=12，联合限制下无解
const fewJudges: Judge[] = [
  { id: 'A', specialties: ['X'] },
  { id: 'B', specialties: ['X'] },
  { id: 'C', specialties: ['X'] },
  { id: 'D', specialties: ['X'] },
];
const fiveWorks: Work[] = ['P1', 'P2', 'P3', 'P4', 'P5'].map((id) => ({ id, specialty: 'X' }));
printResult('场景 3：容量见证（评委对联合限制）', solve(fewJudges, fiveWorks));

// 同一实例放宽联合限制到 3 即可行 —— 说明联合限制确实被全局建模
printResult('场景 3 对照：联合限制放宽为 3', solve(fewJudges, fiveWorks, [], { maxPairShared: 3 }));

// 场景 4：规模限制
const thirteen: Work[] = Array.from({ length: 13 }, (_, i) => ({ id: `Q${i}`, specialty: 'X' }));
printResult('场景 4：13 份作品（超过规模上限）', solve(fewJudges, thirteen));

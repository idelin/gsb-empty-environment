import { Problem, solve, verifyAssignment } from '../src';

// 场景一：12 份作品、3 个专业、9 名评委，带回避关系
const problem: Problem = {
  works: [
    { id: 'w01', field: '结构' }, { id: 'w02', field: '结构' },
    { id: 'w03', field: '结构' }, { id: 'w04', field: '结构' },
    { id: 'w05', field: '材料' }, { id: 'w06', field: '材料' },
    { id: 'w07', field: '材料' }, { id: 'w08', field: '材料' },
    { id: 'w09', field: '岩土' }, { id: 'w10', field: '岩土' },
    { id: 'w11', field: '岩土' }, { id: 'w12', field: '岩土' },
  ],
  judges: [
    { id: 'j1', fields: ['结构'] }, { id: 'j2', fields: ['结构'] },
    { id: 'j3', fields: ['结构', '材料'] }, { id: 'j4', fields: ['材料'] },
    { id: 'j5', fields: ['材料'] }, { id: 'j6', fields: ['材料', '岩土'] },
    { id: 'j7', fields: ['岩土'] }, { id: 'j8', fields: ['岩土'] },
    { id: 'j9', fields: ['结构', '岩土'] },
  ],
  conflicts: [
    { judgeId: 'j1', workId: 'w02' },
    { judgeId: 'j4', workId: 'w05' },
    { judgeId: 'j7', workId: 'w12' },
  ],
};

const result = solve(problem);
if (result.status === 'ok') {
  console.log('== 场景一：求解成功 ==');
  console.log(`工作量最大差（已最小化）: ${result.maxLoadDiff}`);
  console.log('各作品评委分配:');
  for (const [wid, js] of Object.entries(result.assignment)) {
    console.log(`  ${wid}: ${js.join(', ')}`);
  }
  console.log('各评委工作量:', JSON.stringify(result.loads));
  const v = verifyAssignment(problem, result.assignment);
  console.log(`独立校验: ${v.valid ? '通过' : '失败：' + v.violations.join('；')}`);
} else {
  console.log('场景一未解:', JSON.stringify(result, null, 2));
}

// 场景二：评委不足，演示容量见证（评委对联合限制）
const tooFew: Problem = {
  works: Array.from({ length: 12 }, (_, i) => ({ id: `w${i + 1}`, field: 'f' })),
  judges: Array.from({ length: 4 }, (_, i) => ({ id: `j${i + 1}`, fields: ['f'] })),
};
const r2 = solve(tooFew);
console.log('\n== 场景二：无解见证 ==');
if (r2.status === 'infeasible') {
  console.log(`见证类型: ${r2.witness.type}`);
  console.log(`说明: ${r2.witness.detail}`);
}

// 场景三：回避过强，演示回避见证
const conflicted: Problem = {
  works: [{ id: 'w1', field: 'f' }],
  judges: [
    { id: 'j1', fields: ['f'] }, { id: 'j2', fields: ['f'] },
    { id: 'j3', fields: ['f'] }, { id: 'j4', fields: ['g'] },
  ],
  conflicts: [{ judgeId: 'j1', workId: 'w1' }],
};
const r3 = solve(conflicted);
console.log('\n== 场景三：回避见证 ==');
if (r3.status === 'infeasible') {
  console.log(`见证类型: ${r3.witness.type}`);
  console.log(`说明: ${r3.witness.detail}`);
}

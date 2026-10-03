// 运行样例：相关子查询 -> Hash Semi-Join

import { rewrite } from './src/index.ts';

const samples = [
  // 验收用例
  'SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)',
  // IN 子查询（含局部过滤条件）
  "SELECT * FROM depts d WHERE d.id IN (SELECT e.dept_id FROM emps e WHERE e.salary > 5000)",
  // NOT EXISTS -> Anti Join
  'SELECT d.id FROM depts d WHERE NOT EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)',
  // 相关 EXISTS + 外层残余谓词
  "SELECT * FROM depts d WHERE d.region = 'APAC' AND EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id AND e.active = 1)",
];

for (const sql of samples) {
  const r = rewrite(sql);
  console.log('原始 SQL:');
  console.log('  ' + r.original);
  console.log('重写后 SQL:');
  console.log('  ' + r.rewritten);
  console.log('物理执行计划:');
  console.log(
    r.planText
      .split('\n')
      .map((l) => '  ' + l)
      .join('\n'),
  );
  console.log('─'.repeat(72));
}

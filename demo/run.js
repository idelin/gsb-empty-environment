'use strict';
/**
 * Demo: correlated subquery -> decorrelated Hash Semi/Anti Join.
 * Run with: npm run demo   (or: node demo/run.js)
 */

const {
  parse,
  unnest,
  executeBaseline,
  executePlan,
} = require('../lib');

const db = {
  depts: [
    { id: 1, name: 'Engineering' },
    { id: 2, name: 'Sales' },
    { id: 3, name: 'HR' },
    { id: 4, name: 'Legal' },
  ],
  emps: [
    { id: 101, dept_id: 1, name: 'Alice', salary: 120000 },
    { id: 102, dept_id: 1, name: 'Bob', salary: 95000 },
    { id: 103, dept_id: 2, name: 'Carol', salary: 80000 },
    { id: 104, dept_id: 2, name: 'Dave', salary: 72000 },
    { id: 105, dept_id: 2, name: 'Erin', salary: 110000 },
  ],
};

function show(title, sql) {
  console.log('='.repeat(72));
  console.log(title);
  console.log('='.repeat(72));
  console.log(`Original SQL:\n  ${sql}\n`);

  const { rewritten, changed, explain, plan, result } = unnest(sql);
  for (const note of result.notes) console.log(`  [rewrite] ${note}`);
  console.log(`\nRewritten SQL:\n  ${rewritten}\n`);
  console.log(`Physical plan:\n${explain}\n`);

  const stmt = parse(sql);
  const baseline = executeBaseline(stmt, db);
  console.log(
    `Baseline (correlated nested loop): rows=${baseline.rows.length}, ` +
      `subqueryExecutions=${baseline.stats.subqueryExecutions}, ` +
      `rowsExamined=${baseline.stats.rowsExamined}`,
  );

  if (changed && plan) {
    const optimized = executePlan(plan, db);
    console.log(
      `Optimized (decorrelated):          rows=${optimized.rows.length}, ` +
        `subqueryExecutions=${optimized.stats.subqueryExecutions}, ` +
        `rowsExamined=${optimized.stats.rowsExamined}, ` +
        `hashBuilds=${optimized.stats.hashBuilds}`,
    );
    const same =
      JSON.stringify(baseline.rows) === JSON.stringify(optimized.rows);
    console.log(`Results identical: ${same}`);
    console.log('Result rows:', JSON.stringify(optimized.rows));
  }
  console.log();
}

show(
  '1. WHERE EXISTS  ->  Hash Semi Join (acceptance case)',
  'SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)',
);

show(
  '2. WHERE NOT EXISTS  ->  Hash Anti Join',
  'SELECT * FROM depts d WHERE NOT EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)',
);

show(
  '3. Correlated IN with inner-local filter  ->  Hash Semi Join + Filter',
  "SELECT * FROM depts d WHERE d.id IN (SELECT e.dept_id FROM emps e WHERE e.salary > 100000)",
);

show(
  '4. Uncorrelated IN  ->  Hash Semi Join',
  'SELECT * FROM emps e WHERE e.dept_id IN (SELECT d.id FROM depts d)',
);

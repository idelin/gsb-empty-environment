import { strict as assert } from 'assert';
import {
  checkExportPlan,
  effectiveMinGroupSize,
  formatChain,
  FieldDef,
  Sensitivity,
  Violation,
} from '../src';

const S = Sensitivity;

let passed = 0;
const failures: string[] = [];

function test(name: string, fn: () => void): void {
  try {
    fn();
    passed += 1;
    console.log(`ok - ${name}`);
  } catch (err) {
    failures.push(name);
    console.error(`FAIL - ${name}`);
    console.error(err instanceof Error ? err.message : err);
  }
}

function violationsOfType(result: { violations: Violation[] }, type: Violation['type']): Violation[] {
  return result.violations.filter((v) => v.type === type);
}

// ---------- 基础数据集 ----------
// salary(敏感, 1000人) -> dept_avg_salary(按部门聚合, 20组, k=10, 降级到内部)
const salaryFields: FieldDef[] = [
  { kind: 'source', name: 'salary', sensitivity: S.Sensitive, population: 1000 },
  {
    kind: 'aggregate',
    name: 'dept_avg_salary',
    input: 'salary',
    groupCount: 20,
    minGroupSize: 10,
    downgradeTo: S.Internal,
  },
];

test('敏感级别沿派生依赖传播（取输入最大值）', () => {
  const fields: FieldDef[] = [
    { kind: 'source', name: 'ssn', sensitivity: S.Sensitive, population: 500 },
    { kind: 'source', name: 'city', sensitivity: S.Internal, population: 500 },
    { kind: 'derive', name: 'ssn_last4', inputs: ['ssn'], expression: 'right(ssn, 4)' },
    { kind: 'derive', name: 'tag', inputs: ['ssn_last4', 'city'], expression: 'concat' },
  ];
  const result = checkExportPlan(fields, {
    name: 'p1',
    exportFields: ['tag'],
    maxAllowedSensitivity: S.Confidential,
  });
  assert.equal(result.ok, false);
  const report = result.exportedFields[0]!;
  assert.equal(report.effectiveSensitivity, S.Sensitive, '派生字段应继承最高敏感级别');
  const exceeded = violationsOfType(result, 'SENSITIVITY_EXCEEDED');
  assert.equal(exceeded.length, 1);
  const chainFields = exceeded[0]!.chains[0]!.steps.map((s) => s.field);
  assert.deepEqual(chainFields, ['ssn', 'ssn_last4', 'tag'], '来源链应完整记录传播路径');
});

test('聚合达到最小分组人数后可降级', () => {
  const result = checkExportPlan(salaryFields, {
    name: 'p2',
    exportFields: ['dept_avg_salary'],
    maxAllowedSensitivity: S.Internal,
  });
  assert.equal(result.ok, true, `不应有违规: ${JSON.stringify(result.violations)}`);
  const report = result.exportedFields[0]!;
  assert.equal(report.effectiveSensitivity, S.Internal);
  assert.equal(report.population, 20, '聚合后人数为分组数');
  assert.equal(report.chains[0]!.source, 'dept_avg_salary', '降级后来源链从聚合节点重新开始');
  assert.ok(formatChain(report.chains[0]!).includes('聚合降级生效'));
});

test('聚合未达到最小分组人数时禁止降级', () => {
  const fields: FieldDef[] = [
    { kind: 'source', name: 'salary', sensitivity: S.Sensitive, population: 1000 },
    {
      kind: 'aggregate',
      name: 'team_avg_salary',
      input: 'salary',
      groupCount: 200, // floor(1000/200)=5 < k=10
      minGroupSize: 10,
      downgradeTo: S.Internal,
    },
  ];
  const result = checkExportPlan(fields, {
    name: 'p3',
    exportFields: ['team_avg_salary'],
    maxAllowedSensitivity: S.Internal,
  });
  assert.equal(result.ok, false);
  const belowMin = violationsOfType(result, 'AGGREGATION_BELOW_MIN_GROUP');
  assert.equal(belowMin.length, 1);
  assert.ok(belowMin[0]!.message.includes('最小组人数 5'));
  const report = result.exportedFields[0]!;
  assert.equal(report.effectiveSensitivity, S.Sensitive, '降级未生效，敏感级别继续传播');
  assert.ok(violationsOfType(result, 'SENSITIVITY_EXCEEDED').length === 1);
});

test('隐藏原字段不能消除派生字段泄露', () => {
  const fields: FieldDef[] = [
    { kind: 'source', name: 'id_card', sensitivity: S.Sensitive, population: 800 },
    { kind: 'derive', name: 'id_card_hash', inputs: ['id_card'], expression: 'md5(id_card)' },
    { kind: 'derive', name: 'birth_year', inputs: ['id_card'], expression: 'substr(id_card, 7, 4)' },
  ];
  // 导出计划刻意不导出原始字段 id_card，只导出派生字段
  const result = checkExportPlan(fields, {
    name: 'p4',
    exportFields: ['id_card_hash', 'birth_year'],
    maxAllowedSensitivity: S.Confidential,
  });
  assert.equal(result.ok, false);
  const exceeded = violationsOfType(result, 'SENSITIVITY_EXCEEDED');
  assert.deepEqual(
    exceeded.map((v) => v.field).sort(),
    ['birth_year', 'id_card_hash'],
    '两个派生字段都应被标记，即使原字段未导出',
  );
  for (const v of exceeded) {
    assert.equal(v.chains[0]!.source, 'id_card', '来源链应追溯到被隐藏的原始字段');
  }
});

test('过滤后人数减少并可能导致聚合降级失效', () => {
  const fields: FieldDef[] = [
    ...salaryFields.slice(0, 1),
    {
      kind: 'filter',
      name: 'cn_salary',
      input: 'salary',
      retainedPopulation: 120,
      predicate: "region = 'CN'",
    },
    {
      kind: 'aggregate',
      name: 'cn_dept_avg_salary',
      input: 'cn_salary',
      groupCount: 20, // floor(120/20)=6 < k=10
      minGroupSize: 10,
      downgradeTo: S.Internal,
    },
  ];
  const result = checkExportPlan(fields, {
    name: 'p5',
    exportFields: ['cn_dept_avg_salary'],
    maxAllowedSensitivity: S.Internal,
  });
  const filterReport = result.exportedFields[0]!;
  assert.equal(filterReport.effectiveSensitivity, S.Sensitive);
  // 过滤确实减少了人数：来源链上 filter 步骤记录 1000 -> 120
  const chainText = formatChain(
    violationsOfType(result, 'AGGREGATION_BELOW_MIN_GROUP')[0]!.chains[0]!,
  );
  assert.ok(chainText.includes('1000 → 120'), '来源链应体现过滤后人数减少');
  assert.equal(violationsOfType(result, 'AGGREGATION_BELOW_MIN_GROUP').length, 1);
});

test('过滤保留人数大于输入人数属于违规', () => {
  const fields: FieldDef[] = [
    { kind: 'source', name: 'salary', sensitivity: S.Sensitive, population: 1000 },
    { kind: 'filter', name: 'bad_filter', input: 'salary', retainedPopulation: 1500 },
  ];
  const result = checkExportPlan(fields, {
    name: 'p6',
    exportFields: ['bad_filter'],
    maxAllowedSensitivity: S.Sensitive,
  });
  const noReduction = violationsOfType(result, 'FILTER_NO_REDUCTION');
  assert.equal(noReduction.length, 1);
  assert.ok(noReduction[0]!.message.includes('必须减少'));
});

test('共享计算节点只求值一次且各来源链经过它', () => {
  const fields: FieldDef[] = [
    { kind: 'source', name: 'patient_id', sensitivity: S.Sensitive, population: 500 },
    { kind: 'derive', name: 'anon_key', inputs: ['patient_id'], expression: 'hmac(patient_id)' },
    { kind: 'derive', name: 'key_hash_a', inputs: ['anon_key'], expression: 'sha256(anon_key, a)' },
    { kind: 'derive', name: 'key_hash_b', inputs: ['anon_key'], expression: 'sha256(anon_key, b)' },
  ];
  const result = checkExportPlan(fields, {
    name: 'p7',
    exportFields: ['key_hash_a', 'key_hash_b'],
    maxAllowedSensitivity: S.Confidential,
  });
  assert.deepEqual(result.stats.sharedNodes, ['anon_key'], 'anon_key 是共享计算节点');
  assert.equal(
    result.stats.evaluatedNodes,
    4,
    'patient_id/anon_key/key_hash_a/key_hash_b 各求值一次，共享节点不重复计算',
  );
  const exceeded = violationsOfType(result, 'SENSITIVITY_EXCEEDED');
  assert.equal(exceeded.length, 2);
  for (const v of exceeded) {
    const chainFields = v.chains[0]!.steps.map((s) => s.field);
    assert.ok(chainFields.includes('anon_key'), `${v.field} 的来源链应经过共享节点 anon_key`);
    assert.equal(chainFields[0], 'patient_id');
  }
});

test('实测最小组人数参与保守取小', () => {
  const agg = {
    kind: 'aggregate' as const,
    name: 'a',
    input: 'x',
    groupCount: 10,
    minGroupSize: 5,
    downgradeTo: S.Internal,
    observedMinGroupSize: 3, // 实测最小组只有 3 人
  };
  // 均匀下界 floor(1000/10)=100，但实测 3，取小者
  assert.equal(effectiveMinGroupSize(agg, 1000), 3);
  assert.equal(
    effectiveMinGroupSize({ ...agg, observedMinGroupSize: undefined }, 1000),
    100,
  );
});

test('未知字段与循环依赖被报告', () => {
  const fields: FieldDef[] = [
    { kind: 'derive', name: 'a', inputs: ['b'] },
    { kind: 'derive', name: 'b', inputs: ['a'] },
  ];
  const result = checkExportPlan(fields, {
    name: 'p8',
    exportFields: ['a', 'ghost'],
    maxAllowedSensitivity: S.Sensitive,
  });
  assert.equal(violationsOfType(result, 'UNKNOWN_FIELD').length, 1);
  assert.equal(violationsOfType(result, 'CYCLIC_DEPENDENCY').length >= 1, true);
  assert.equal(result.ok, false);
});

console.log(`\n${passed} passed, ${failures.length} failed`);
if (failures.length > 0) {
  console.error(`failed: ${failures.join(', ')}`);
  process.exit(1);
}

import { suggestRepairs, applyPlan, compileSchema, type JsonObject } from '../src/index.ts';

const schema: Record<string, unknown> = {
  type: 'object',
  properties: {
    count: { type: 'integer' },
    active: { type: 'boolean' },
    level: { type: 'string', enum: ['LOW', 'MEDIUM', 'HIGH'] },
    code: { type: 'string', enum: ['A', 'B', 'C'] },
    minAmount: { type: 'integer' },
    maxAmount: {
      type: 'integer',
      requires: ['minAmount'],
      compare: [{ field: 'minAmount', op: '>=' }],
    },
  },
  required: ['count', 'level'],
};

function show(title: string, instance: JsonObject, budget = 20000): void {
  console.log('\n================ ' + title + ' ================');
  console.log('输入:', JSON.stringify(instance));
  const res = suggestRepairs(schema, instance, { budget });
  console.log('状态:', res.status);
  console.log(
    '错误（实例路径 -> 规则路径）:',
    res.errors.map((e) => `${e.instancePath} [${e.kind}] <- ${e.rulePath}`),
  );
  console.log(
    '候选组:',
    res.groups.map(
      (g) =>
        `${g.targetPath}: ` +
        g.candidates
          .map((c) => `${c.op === 'unset' ? '删除' : '置 ' + JSON.stringify(c.value)}(${c.certainty === 'certain' ? '确定' : '人工'}¥${c.cost}${c.pending ? ',pending' : ''})`)
          .join(' | '),
    ),
  );
  console.log(`穷举：组合总数=${res.combinationsTotal}，已核验=${res.combinationsChecked}，预算=${res.budget.limit}`);
  console.log(`最优代价=${res.optimalCost ?? '未证明'}，可行上界=${res.upperBoundCost}`);
  console.log(`同成本最优方案数=${res.bestPlans.length}，未决人工路径=${JSON.stringify(res.pendingGroups)}`);
  res.bestPlans.forEach((p, i) => {
    console.log(
      `  方案${i + 1} [¥${p.totalCost}${p.containsPending ? ', 含pending' : ''}]: ` +
        p.patches.map((q) => `${q.targetPath} ${q.op}${q.value !== undefined ? '=' + JSON.stringify(q.value) : ''} <- ${q.originRulePath}`).join('；'),
    );
  });
  if (res.unexploredGroups.length > 0) {
    console.log('预算中断，未完整展开的组:', res.unexploredGroups);
  }
}

// 1) 多错误并存：不能只修最先报错字段；确定/人工分级；'001' 需人工
show('多错误 + 前导零需人工 + 枚举并列', {
  count: '001',
  active: 'true',
  level: 'MEDIM',
  code: 'D',
});

// 2) 跨字段 requires：补字段(pending ¥100) vs 删源字段(¥15)，选全局最小代价
show('跨字段 requires：删字段路线更优', {
  count: 3,
  level: 'LOW',
  maxAmount: 10,
});

// 3) 跨字段 compare 数字冲突：改任一侧两个同成本平局方案全部保留
show('跨字段 compare：两个同成本最优方案', {
  count: 3,
  level: 'LOW',
  minAmount: 100,
  maxAmount: 50,
});

// 4) 原子应用 + 应用后重新验证；原始输入不变
const input: JsonObject = { count: '12', level: 'LOW' };
const res = suggestRepairs(schema, input);
const plan = res.bestPlans.find((p) => p.status === 'legal')!;
const applied = applyPlan(input, compileSchema(schema), plan);
console.log('\n================ 原子应用 ================');
console.log('应用结果 ok =', applied.ok, '| 原始输入保持不变 =', JSON.stringify(input) === JSON.stringify({ count: '12', level: 'LOW' }));
console.log('应用后实例 =', JSON.stringify(applied.output), '| 回滚 =', applied.rolledBack);
console.log('对应用后实例重新 suggestRepairs =>', suggestRepairs(schema, applied.output!).status);

// 5) 预算中断：返回可行上界与未决
show('预算中断（budget=2）', { count: '001', level: 'LOW', code: 'ZZZ' }, 2);

import { Solver, validate, type Schema } from '../src/index.ts';

function show(title: string, instance: unknown, schema: Schema, options = {}) {
  const result = validate(instance, schema, options);
  console.log(`\n=== ${title} ===`);
  console.log('instance:', JSON.stringify(instance));
  console.log(JSON.stringify(result, null, 2));
}

// 1) 三种字段组 + 存在 != 真值:false/null 仍算存在
show(
  'presence != truthiness (false/null count as present)',
  { password: false, email: null },
  {
    groups: [
      { kind: 'exactlyOne', fields: ['password', 'token'] },
      { kind: 'atLeastOne', fields: ['email', 'phone'] },
      { kind: 'atMostK', k: 1, fields: ['nick', 'avatar', 'bio'] },
    ],
  },
);

// 2) 条件启用:ssl=true 时才要求 cert/key 至少其一
show(
  'conditional enablement',
  { ssl: true },
  {
    groups: [
      {
        kind: 'atLeastOne',
        fields: ['cert', 'key'],
        when: { op: 'equals', field: 'ssl', value: true },
      },
    ],
  },
);

// 3) 条件引用缺失字段 -> 未决,按契约策略 'error' 处理
show(
  'undecided condition with policy "error"',
  {},
  {
    groups: [
      {
        kind: 'atLeastOne',
        fields: ['cert'],
        when: { op: 'equals', field: 'ssl', value: true },
      },
    ],
  },
  { onUndecided: 'error' },
);

// 4) 交叠组 + 条件启用:单组各自可行,联合不可行 -> 子集最小冲突核
show(
  'jointly unsat overlapping conditional groups -> minimal core',
  { mode: 'strict' },
  {
    groups: [
      { kind: 'atLeastOne', fields: ['b'], when: { op: 'equals', field: 'mode', value: 'strict' } },
      { kind: 'atMostK', k: 0, fields: ['b'], when: { op: 'equals', field: 'mode', value: 'strict' } },
    ],
  },
);

// 5) 最小补字段集合:交叠组共享见证 {b}
show(
  'minimal completion shared by overlapping groups',
  {},
  {
    groups: [
      { kind: 'exactlyOne', fields: ['a', 'b'] },
      { kind: 'atLeastOne', fields: ['b', 'c'] },
    ],
  },
);

// 6) 补字段通过"关闭"条件组生效(条件非单调)
show(
  'completion that disables a conditional group',
  {},
  {
    groups: [
      { kind: 'atLeastOne', fields: ['b'] },
      { kind: 'atMostK', k: 0, fields: ['b'], when: { op: 'absent', field: 'c' } },
    ],
  },
);

// 7) push/pop:局部修改后恢复传播状态
{
  console.log('\n=== solver push/pop restores propagation state ===');
  const solver = new Solver();
  const a = solver.addVar();
  const b = solver.addVar();
  solver.addConstraint({ id: 'exactlyOne(a,b)', vars: [a, b], min: 1, max: 1 });
  solver.push();
  solver.assign(a, 1);
  solver.propagate();
  console.log('inside scope after propagate: a =', solver.value(a), ', b =', solver.value(b));
  solver.pop();
  console.log('after pop: a =', solver.value(a), ', b =', solver.value(b), '(both unassigned again)');
}

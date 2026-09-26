import {
  ABSENT,
  Solver,
  findWitness,
  jointSatisfiable,
  materialize,
  minimalCorrections,
  minimalUnsatCore,
  validate,
  type Schema,
} from '../src/index.ts';

const show = (title: string, value: unknown): void => {
  console.log(`\n=== ${title} ===`);
  console.log(JSON.stringify(value, null, 2));
};

// 1. 字段存在 != 真值：false / null 仍算存在
const presenceSchema: Schema = {
  groups: [{ kind: 'exactlyOne', fields: ['card', 'paypal'] }],
};
show('false counts as present (valid)', validate({ card: false }, presenceSchema));
show('false + null both present (conflict)', validate({ card: false, paypal: null }, presenceSchema));

// 2. 条件启用 + 未决策略
const condSchema: Schema = {
  onUndecidedCondition: 'error',
  groups: [
    {
      kind: 'exactlyOne',
      fields: ['card', 'paypal'],
      when: { op: 'equals', field: 'mode', value: 'payment' },
    },
  ],
};
show('condition undecided -> policy error', validate({}, condSchema));
show('condition active, group violated', validate({ mode: 'payment' }, condSchema));
show('condition inactive, group skipped', validate({ mode: 'other' }, condSchema));

// 3. 重叠组 + 条件启用：单组各自可行，联合不可行
const jointSchema: Schema = {
  groups: [
    { kind: 'exactlyOne', fields: ['a', 'b'], when: { op: 'present', field: 'c' } },
    { kind: 'atMost', k: 0, fields: ['a', 'b'], when: { op: 'present', field: 'c' } },
    { kind: 'atLeastOne', fields: ['c'] },
  ],
};
show('each group alone is satisfiable', [
    jointSatisfiable({ groups: [jointSchema.groups![0]] }),
    jointSatisfiable({ groups: [jointSchema.groups![1]] }),
    jointSatisfiable({ groups: [jointSchema.groups![2]] }),
]);
show('joint satisfiability', jointSatisfiable(jointSchema));
show('subset-minimal unsat core (original rule paths)', minimalUnsatCore(jointSchema)?.rulePaths);

// 4. 最小补字段集合（缺失 / 冲突修复）
const fixSchema: Schema = {
  groups: [
    { kind: 'exactlyOne', fields: ['a', 'b'] },
    { kind: 'atMost', k: 0, fields: ['b'] },
  ],
};
show('minimal corrections for { b: 1 }', minimalCorrections(fixSchema, { b: 1 }));

// 5. 小域枚举：列出全部见证并逐一验证
const enumSchema: Schema = {
  groups: [
    { kind: 'exactlyOne', fields: ['a', 'b', 'c'] },
    {
      kind: 'atLeastOne',
      fields: ['x'],
      when: { op: 'equals', field: 'mode', value: 'strict' },
    },
  ],
};
const solver = new Solver(enumSchema);
const witnesses = solver.enumerate();
show('witness count', witnesses.length);
for (const w of witnesses) {
  const instance = materialize(w.assignment, solver);
  console.log('witness', JSON.stringify(instance), 'valid =', validate(instance, enumSchema).valid);
}

// 6. push/pop：局部修改后恢复传播状态
const pushPop = new Solver({ groups: [{ kind: 'exactlyOne', fields: ['a', 'b', 'c'] }] });
console.log('\n=== push/pop ===');
console.log('baseline witnesses:', pushPop.enumerate().length);
pushPop.push();
pushPop.assume('a', ABSENT);
console.log('with a assumed absent:', pushPop.enumerate().length);
pushPop.pop();
console.log('after push/assume/pop, witnesses:', pushPop.enumerate().length);

show('one witness for the enum schema', findWitness(enumSchema)?.present);

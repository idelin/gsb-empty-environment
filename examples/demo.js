// @ts-check
import {
  checkMatch,
  crossValidate,
  computeMinDepth,
  enumerateValues,
  ctor,
  wild,
  v,
  prettyPattern,
  prettyValue,
} from '../src/index.js';

const _ = wild();

/** @param {string} title */
function section(title) {
  console.log(`\n${'='.repeat(64)}\n${title}\n${'='.repeat(64)}`);
}

/**
 * @param {import('../src/schema.js').AdtSchema} schema
 * @param {string} type
 * @param {import('../src/pattern.js').Pattern[]} branches
 * @param {number} depth
 */
function report(schema, type, branches, depth) {
  console.log('分支（按匹配优先级）:');
  branches.forEach((p, i) => console.log(`  #${i}  ${prettyPattern(p)}`));
  const r = checkMatch(schema, type, branches);
  console.log('\n算法判定:');
  if (r.exhaustive) {
    console.log(`  穷尽: 是${r.vacuous ? '（vacuous：缺失模式仅经过不可驻留类型，无具体缺失值）' : ''}`);
  } else {
    console.log('  穷尽: 否');
    console.log(`  最小深度见证: 模式 ${prettyPattern(r.witness.pattern)}`);
    console.log(`               值   ${prettyValue(r.witness.value)}  (构造深度 ${r.witness.depth})`);
  }
  console.log(
    `  冗余分支: ${r.redundant.length === 0 ? '无' : r.redundant.map((i) => `#${i} ${prettyPattern(branches[i])}`).join(', ')}`,
  );

  const cv = crossValidate(schema, type, branches, depth);
  console.log(`\n深度受限枚举交叉验证 (深度 <= ${depth}, 共 ${cv.valueCount} 个值):`);
  console.log(`  未覆盖值: ${cv.uncovered.length === 0 ? '无' : `${cv.uncovered.length} 个`}`);
  const show = cv.uncovered.slice(0, 4);
  for (const value of show) console.log(`    ${prettyValue(value)}`);
  if (cv.uncovered.length > show.length) console.log(`    ... 其余 ${cv.uncovered.length - show.length} 个略`);
  console.log('  分支可达性:');
  branches.forEach((p, i) => {
    const tag = cv.branchHits[i] > 0 ? `可达（${cv.branchHits[i]} 次命中）` : '不可达（深度内零命中）';
    const mark = r.redundant.includes(i) ? '  <= 算法判定冗余' : '';
    console.log(`    #${i}  ${prettyPattern(p)}  ${tag}${mark}`);
  });
  for (const note of cv.notes) console.log(`  说明: ${note}`);
  console.log(`  交叉验证: ${cv.inconsistencies.length === 0 ? '一致 ✓' : `不一致: ${cv.inconsistencies.join('; ')}`}`);
}

/** @type {import('../src/schema.js').AdtSchema} */
const boolSchema = {
  types: {
    Bool: [
      { name: 'True', args: [] },
      { name: 'False', args: [] },
    ],
  },
};

/** @type {import('../src/schema.js').AdtSchema} */
const listSchema = {
  types: {
    Bool: [
      { name: 'True', args: [] },
      { name: 'False', args: [] },
    ],
    List: [
      { name: 'Nil', args: [] },
      { name: 'Cons', args: ['Bool', 'List'] },
    ],
  },
};

/** @type {import('../src/schema.js').AdtSchema} */
const exprSchema = {
  types: {
    Expr: [
      { name: 'Num', args: [] },
      { name: 'Neg', args: ['Expr'] },
      { name: 'Add', args: ['Expr', 'Expr'] },
    ],
  },
};

section('示例 1: Bool —— 非穷尽，给出最小深度见证');
report(boolSchema, 'Bool', [ctor('True')], 2);

section('示例 2: List（递归类型）—— 缺失见证 + 冗余分支 + 交叉验证');
report(
  listSchema,
  'List',
  [
    ctor('Nil'),
    ctor('Cons', ctor('True'), ctor('Cons', _, _)),
    ctor('Cons', ctor('True'), ctor('Cons', ctor('False'), _)), // 被 #1 完全覆盖
  ],
  4,
);

section('示例 3: List —— 补全后穷尽（变量模式 x 与通配符同义）');
report(
  listSchema,
  'List',
  [
    ctor('Nil'),
    ctor('Cons', ctor('True'), ctor('Cons', _, _)),
    ctor('Cons', ctor('False'), v('tail')),
    ctor('Cons', ctor('True'), ctor('Nil')),
  ],
  4,
);

section('示例 4: Expr —— 嵌套模式的最小见证 Neg(Num)');
report(
  exprSchema,
  'Expr',
  [ctor('Num'), ctor('Add', ctor('Num'), ctor('Num')), ctor('Neg', ctor('Neg', _))],
  3,
);

section('示例 5: 递归不可驻留类型 Loop = Wrap(Loop) —— 不无限展开');
{
  /** @type {import('../src/schema.js').AdtSchema} */
  const loopSchema = { types: { Loop: [{ name: 'Wrap', args: ['Loop'] }] } };
  const depths = computeMinDepth(loopSchema);
  console.log(`最小驻留深度（不动点计算，有限步收敛）: Loop = ${depths.typeDepth.Loop}`);
  console.log(`深度 <= 5 的枚举值个数: ${enumerateValues(loopSchema, 'Loop', 5).length}`);
  report(loopSchema, 'Loop', [], 5);
}

section('示例 6: 递归类型按深度截断的枚举规模（List）');
for (const d of [1, 2, 3, 4, 5]) {
  console.log(`  深度 <= ${d}: ${enumerateValues(listSchema, 'List', d).length} 个值`);
}

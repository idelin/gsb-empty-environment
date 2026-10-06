// @ts-check
import {
  checkMatch,
  crossValidate,
  enumerateValues,
  computeMinDepth,
  ctor,
  wild,
  v,
  prettyValue,
  valueDepth,
} from '../src/index.js';

let passed = 0;
let failed = 0;

/**
 * @param {string} name
 * @param {() => void} fn
 */
function test(name, fn) {
  try {
    fn();
    passed++;
    console.log(`ok   - ${name}`);
  } catch (err) {
    failed++;
    console.error(`FAIL - ${name}`);
    console.error(String(err && err.stack ? err.stack : err));
  }
}

/**
 * @param {unknown} actual
 * @param {unknown} expected
 */
function eq(actual, expected) {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  if (a !== e) throw new Error(`expected ${e}, got ${a}`);
}

/** @param {unknown} cond @param {string} msg */
function ok(cond, msg) {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

const _ = wild();

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

test('Bool: 单分支 True 非穷尽，见证为 False（深度 1）', () => {
  const r = checkMatch(boolSchema, 'Bool', [ctor('True')]);
  eq(r.exhaustive, false);
  eq(prettyValue(r.witness.value), 'False');
  eq(r.witness.depth, 1);
  eq(r.redundant, []);
});

test('Bool: True/False 两分支穷尽', () => {
  const r = checkMatch(boolSchema, 'Bool', [ctor('True'), ctor('False')]);
  eq(r.exhaustive, true);
  eq(r.witness, null);
});

test('Bool: 通配符兜底穷尽；其后的分支冗余', () => {
  const r = checkMatch(boolSchema, 'Bool', [ctor('True'), _, ctor('False')]);
  eq(r.exhaustive, true);
  eq(r.redundant, [2]);
});

test('Bool: 变量模式与通配符语义相同', () => {
  const r = checkMatch(boolSchema, 'Bool', [v('x'), ctor('True')]);
  eq(r.exhaustive, true);
  eq(r.redundant, [1]);
});

test('List(递归): 仅 Cons(_, Nil) 时最小见证是 Nil（深度 1，不无限展开）', () => {
  const r = checkMatch(listSchema, 'List', [ctor('Cons', _, ctor('Nil'))]);
  eq(r.exhaustive, false);
  eq(prettyValue(r.witness.value), 'Nil');
  eq(r.witness.depth, 1);
});

test('List(递归): 缺 Cons(False,_) 与 Cons(True,Nil)，最小见证深度 2', () => {
  const branches = [
    ctor('Nil'),
    ctor('Cons', ctor('True'), ctor('Cons', _, _)),
    ctor('Cons', ctor('True'), ctor('Cons', ctor('False'), _)), // 被上一行覆盖
  ];
  const r = checkMatch(listSchema, 'List', branches);
  eq(r.exhaustive, false);
  eq(r.witness.depth, 2);
  eq(prettyValue(r.witness.value), 'Cons(False, Nil)');
  eq(r.redundant, [2]);
});

test('List(递归): 补全后穷尽且无冗余', () => {
  const branches = [
    ctor('Nil'),
    ctor('Cons', ctor('True'), ctor('Cons', _, _)),
    ctor('Cons', ctor('False'), _),
    ctor('Cons', ctor('True'), ctor('Nil')),
  ];
  const r = checkMatch(listSchema, 'List', branches);
  eq(r.exhaustive, true);
  eq(r.witness, null);
  eq(r.redundant, []);
});

test('Expr(嵌套): 缺 Neg(Num)，见证深度 2', () => {
  const branches = [
    ctor('Num'),
    ctor('Add', ctor('Num'), ctor('Num')),
    ctor('Neg', ctor('Neg', _)),
  ];
  const r = checkMatch(exprSchema, 'Expr', branches);
  eq(r.exhaustive, false);
  eq(prettyValue(r.witness.value), 'Neg(Num)');
  eq(r.witness.depth, 2);
});

test('Expr(嵌套): 冗余的深层嵌套分支被检出', () => {
  const branches = [
    ctor('Num'),
    ctor('Neg', ctor('Neg', _)),
    ctor('Neg', ctor('Neg', ctor('Num'))), // 被 #1 覆盖
  ];
  const r = checkMatch(exprSchema, 'Expr', branches);
  eq(r.redundant, [2]);
});

test('递归不可驻留类型 Loop=Wrap(Loop): 不无限展开，vacuous 视为穷尽', () => {
  /** @type {import('../src/schema.js').AdtSchema} */
  const loopSchema = { types: { Loop: [{ name: 'Wrap', args: ['Loop'] }] } };
  const depths = computeMinDepth(loopSchema);
  eq(depths.typeDepth.Loop, Infinity);
  const r = checkMatch(loopSchema, 'Loop', []);
  eq(r.vacuous, true);
  eq(r.exhaustive, true);
  eq(r.witness, null);
});

test('空类型: 零分支即穷尽（空签名完备）', () => {
  /** @type {import('../src/schema.js').AdtSchema} */
  const emptySchema = { types: { Empty: [] } };
  const r = checkMatch(emptySchema, 'Empty', []);
  eq(r.exhaustive, true);
});

test('枚举: List 深度 d 的值数为 2^d - 1', () => {
  for (const d of [1, 2, 3, 4]) {
    eq(enumerateValues(listSchema, 'List', d).length, 2 ** d - 1);
  }
});

test('交叉验证: 三个示例场景均无不一致，不可达分支与冗余判定吻合', () => {
  const cases = [
    {
      schema: listSchema,
      type: 'List',
      branches: [
        ctor('Nil'),
        ctor('Cons', ctor('True'), ctor('Cons', _, _)),
        ctor('Cons', ctor('True'), ctor('Cons', ctor('False'), _)),
      ],
      depth: 4,
    },
    {
      schema: exprSchema,
      type: 'Expr',
      branches: [ctor('Num'), ctor('Add', ctor('Num'), ctor('Num')), ctor('Neg', ctor('Neg', _))],
      depth: 3,
    },
    {
      schema: listSchema,
      type: 'List',
      branches: [
        ctor('Nil'),
        ctor('Cons', ctor('True'), ctor('Cons', _, _)),
        ctor('Cons', ctor('False'), _),
        ctor('Cons', ctor('True'), ctor('Nil')),
      ],
      depth: 4,
    },
  ];
  for (const c of cases) {
    const cv = crossValidate(c.schema, c.type, c.branches, c.depth);
    eq(cv.inconsistencies, []);
    const r = checkMatch(c.schema, c.type, c.branches);
    for (const i of r.redundant) ok(cv.unreachable.includes(i), `冗余分支 #${i} 应不可达`);
  }
});

test('交叉验证: 非穷尽时枚举发现未覆盖值且最浅深度等于见证深度', () => {
  const branches = [ctor('Nil'), ctor('Cons', ctor('True'), ctor('Cons', _, _))];
  const cv = crossValidate(listSchema, 'List', branches, 4);
  ok(cv.uncovered.length > 0, '应有未覆盖值');
  const minDepth = Math.min(...cv.uncovered.map(valueDepth));
  eq(minDepth, 2);
});

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);

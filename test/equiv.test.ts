import assert from 'node:assert/strict';
import test from 'node:test';
import { Compiler, evalNode } from '../src/dag.ts';
import { PolicyEngine } from '../src/engine.ts';
import { and, inRange, inSet, not, or, type Expr } from '../src/expr.ts';
import { triAnd, triNot, triOf, triOr, type Tri } from '../src/logic.ts';
import { AttrStore } from '../src/store.ts';

/** 朴素参考求值器：直接在 AST 上做 Kleene 三值求值，无任何优化。 */
function naive(expr: Expr, get: (attr: string) => number | undefined): Tri {
  switch (expr.kind) {
    case 'lit':
      return triOf(expr.value);
    case 'in': {
      const v = get(expr.attr);
      return v === undefined ? 'unknown' : triOf(expr.values.includes(v));
    }
    case 'range': {
      const v = get(expr.attr);
      if (v === undefined) return 'unknown';
      return triOf(
        (expr.min === null || v >= expr.min) && (expr.max === null || v <= expr.max),
      );
    }
    case 'not':
      return triNot(naive(expr.arg, get));
    case 'and': {
      let acc: Tri = 'true';
      for (const a of expr.args) acc = triAnd(acc, naive(a, get));
      return acc;
    }
    case 'or': {
      let acc: Tri = 'false';
      for (const a of expr.args) acc = triOr(acc, naive(a, get));
      return acc;
    }
  }
}

const POLICY: Expr = or(
  and(inSet('a', [0, 2]), inRange('b', 1, 3)),
  and(not(inSet('a', [1])), inSet('c', [5])),
  inRange('b', 10, 20),
);

// 有限域（含缺失 undefined）
const DOMAIN: Record<string, (number | undefined)[]> = {
  a: [undefined, 0, 1, 2],
  b: [undefined, 1, 5, 15],
  c: [undefined, 5, 6],
};

function* assignments(): Generator<Record<string, number | undefined>> {
  for (const a of DOMAIN.a)
    for (const b of DOMAIN.b)
      for (const c of DOMAIN.c) yield { a, b, c };
}

test('有限域真值枚举：DAG 优化求值与朴素求值完全等价', () => {
  const compiler = new Compiler();
  const root = compiler.compile(POLICY);
  let cases = 0;
  for (const assignment of assignments()) {
    const get = (attr: string) => assignment[attr];
    const expected = naive(POLICY, get);
    const actual = evalNode(root, get);
    assert.equal(actual, expected, `mismatch at ${JSON.stringify(assignment)}`);
    cases++;
  }
  assert.equal(cases, 4 * 4 * 3);
});

test('部分求值：残差条件在一切补全下与完整求值等价', async () => {
  const store = new AttrStore(async () => undefined);
  const engine = new PolicyEngine(store, POLICY);
  let cases = 0;
  for (const assignment of assignments()) {
    // 已知 a，b/c 未知 => 残差只应涉及 b、c
    const { root: residual, remaining } = engine.partial({ a: assignment.a });
    assert.ok(!remaining.includes('a'));
    const full = naive(POLICY, (attr) => assignment[attr]);
    const viaResidual = evalNode(residual, (attr) => assignment[attr]);
    assert.equal(viaResidual, full, `residual mismatch at ${JSON.stringify(assignment)}`);
    cases++;
  }
  assert.equal(cases, 48);
});

test('部分求值：全部已知时残差为常量，且 unknown 不被压成 false', async () => {
  const store = new AttrStore(async () => undefined);
  const engine = new PolicyEngine(store, or(inSet('x', [1]), not(inSet('x', [1]))));
  const missing = engine.partial({ x: undefined });
  assert.equal(missing.remaining.length, 0);
  assert.equal(evalNode(missing.root, () => undefined), 'unknown');
  const present = engine.partial({ x: 1 });
  assert.equal(evalNode(present.root, () => undefined), 'true');
});

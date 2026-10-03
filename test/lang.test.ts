import assert from 'node:assert/strict';
import test from 'node:test';
import { at, resetNodeIds } from '../src/ast';
import { Evaluator } from '../src/grammar';
import {
  binary,
  bool,
  buildLangGrammar,
  Env,
  exprStmt,
  num,
  program,
  varDecl,
  varRef,
} from '../src/lang';

const evaluate = (root: ReturnType<typeof program>) =>
  new Evaluator(buildLangGrammar()).evaluate(root);

test('variable declaration and use: type inference and evaluation', () => {
  resetNodeIds();
  // 1: let x: int = 1 + 2;
  // 2: let y: bool = x < 5;
  // 3: x * 4;
  const decl1 = varDecl('x', 'int', binary('+', num(1, at(1, 16)), num(2, at(1, 20)), at(1, 18)), at(1, 5));
  const decl2 = varDecl('y', 'bool', binary('<', varRef('x', at(2, 17)), num(5, at(2, 21)), at(2, 19)), at(2, 5));
  const stmt3 = exprStmt(binary('*', varRef('x', at(3, 1)), num(4, at(3, 5)), at(3, 3)), at(3, 1));
  const root = program([decl1, decl2, stmt3]);

  const result = evaluate(root);
  assert.equal(result.diagnostics.length, 0);

  // inherited env flows top-down: statement 2 sees x, statement 3 sees x and y
  const env2 = result.get<Env>(decl2, 'env');
  assert.equal(env2.get('x')?.type, 'int');
  assert.equal(env2.has('y'), false);
  const env3 = result.get<Env>(stmt3, 'env');
  assert.equal(env3.get('y')?.type, 'bool');

  // synthesized envOut threads declarations to later siblings
  const envOut2 = result.get<Env>(decl2, 'envOut');
  assert.deepEqual([...envOut2.keys()], ['x', 'y']);
  assert.equal(envOut2.get('x')?.value, 3);

  // synthesized type/value bubble bottom-up
  const mul = stmt3.children[0]!;
  assert.equal(result.get(mul, 'type'), 'int');
  assert.equal(result.get(mul, 'value'), 12);
  const cmp = decl2.children[0]!;
  assert.equal(result.get(cmp, 'type'), 'bool');
  assert.equal(result.get(cmp, 'value'), true);
});

test('declaration type mismatch is reported at the VarDecl node position', () => {
  resetNodeIds();
  // 1: let a: int = true;
  const decl = varDecl('a', 'int', bool(true, at(1, 14)), at(1, 5));
  const result = evaluate(program([decl]));

  assert.equal(result.diagnostics.length, 1);
  const d = result.diagnostics[0]!;
  assert.match(d.message, /type mismatch/);
  assert.match(d.message, /'a'/);
  assert.match(d.message, /'int'.*'bool'/);
  assert.deepEqual(d.pos, { line: 1, column: 5 });
  assert.equal(d.nodeId, decl.id);
  assert.equal(result.get(decl, 'check'), false);
});

test('operator type mismatch is reported at the operator node position', () => {
  resetNodeIds();
  // 3: b + false;
  const decl = varDecl('b', 'int', num(1, at(1, 14)), at(1, 5));
  const add = binary('+', varRef('b', at(3, 1)), bool(false, at(3, 5)), at(3, 3));
  const root = program([decl, exprStmt(add, at(3, 1))]);

  const result = evaluate(root);
  assert.equal(result.diagnostics.length, 1);
  const d = result.diagnostics[0]!;
  assert.match(d.message, /operator '\+'/);
  assert.match(d.message, /\(int, bool\)/);
  assert.deepEqual(d.pos, { line: 3, column: 3 });
  assert.equal(d.nodeId, add.id);
  assert.equal(result.get(add, 'type'), 'error');
  assert.equal(result.get(add, 'value'), undefined);
});

test('undefined variable is reported at the variable node position', () => {
  resetNodeIds();
  // 4: missing;
  const ref = varRef('missing', at(4, 1));
  const result = evaluate(program([exprStmt(ref, at(4, 1))]));

  assert.equal(result.diagnostics.length, 1);
  const d = result.diagnostics[0]!;
  assert.match(d.message, /undefined variable 'missing'/);
  assert.deepEqual(d.pos, { line: 4, column: 1 });
  assert.equal(d.nodeId, ref.id);
  assert.equal(result.get(ref, 'type'), 'error');
});

test('variables are not visible before their declaration (scope ordering)', () => {
  resetNodeIds();
  // 1: x;               -> x not yet declared
  // 2: let x: int = 1;
  const earlyRef = varRef('x', at(1, 1));
  const decl = varDecl('x', 'int', num(1, at(2, 14)), at(2, 5));
  const result = evaluate(program([exprStmt(earlyRef, at(1, 1)), decl]));

  assert.equal(result.diagnostics.length, 1);
  assert.match(result.diagnostics[0]!.message, /undefined variable 'x'/);
  assert.deepEqual(result.diagnostics[0]!.pos, { line: 1, column: 1 });
});

test('mixed expression: boolean logic and comparison operators', () => {
  resetNodeIds();
  // 1: let x: int = 10;
  // 2: (x > 5) && (x == 10);
  const decl = varDecl('x', 'int', num(10, at(1, 14)), at(1, 5));
  const expr = binary(
    '&&',
    binary('>', varRef('x', at(2, 2)), num(5, at(2, 6)), at(2, 4)),
    binary('==', varRef('x', at(2, 12)), num(10, at(2, 17)), at(2, 14)),
    at(2, 9),
  );
  const stmt = exprStmt(expr, at(2, 1));
  const result = evaluate(program([decl, stmt]));

  assert.equal(result.diagnostics.length, 0);
  assert.equal(result.get(expr, 'type'), 'bool');
  assert.equal(result.get(stmt, 'value'), true);
});

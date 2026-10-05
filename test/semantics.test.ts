import test from 'node:test';
import assert from 'node:assert/strict';
import {
  bin,
  block,
  boolLit,
  constDecl,
  exprStmt,
  funcDecl,
  intLit,
  program,
  ref,
  resetIds,
  un,
} from '../src/ast.ts';
import { CheckedProgram } from '../src/program.ts';

test('constant folding and types across visible declarations', () => {
  resetIds();
  const declA = constDecl('a', intLit(1));
  const declB = constDecl('b', bin('+', ref('a'), intLit(2)));
  const declC = constDecl('c', bin('*', ref('b'), ref('b')));
  const cp = new CheckedProgram(program(block([declA, declB, declC])));
  assert.equal(cp.check().length, 0);
  assert.equal(cp.typeOf(declB.id), 'int');
  assert.equal(cp.valueOf(declB.id), 3);
  assert.equal(cp.valueOf(declC.id), 9);
});

test('type errors are located by node id and attribute name', () => {
  resetIds();
  const bad = bin('+', intLit(1), boolLit(true));
  const decl = constDecl('x', bad);
  const cp = new CheckedProgram(program(block([decl])));
  const diags = cp.check();
  assert.equal(diags.length, 1);
  assert.equal(diags[0].nodeId, bad.id);
  assert.equal(diags[0].attr, 'type');
  assert.match(diags[0].message, /operator '\+' expects int operands/);
  assert.equal(cp.typeOf(decl.id), 'error');
  assert.equal(cp.valueOf(decl.id), 'error');
});

test('sequential visibility: a declaration is visible only after itself', () => {
  resetIds();
  const earlyRef = ref('x');
  const stmt = exprStmt(earlyRef);
  const decl = constDecl('x', intLit(1));
  const lateRef = ref('x');
  const cp = new CheckedProgram(program(block([stmt, decl, exprStmt(lateRef)])));
  assert.equal(cp.bindOf(earlyRef.id), null);
  assert.equal(cp.bindOf(lateRef.id), decl.id);
  const diags = cp.check();
  assert.equal(diags.length, 1);
  assert.equal(diags[0].nodeId, earlyRef.id);
  assert.equal(diags[0].attr, 'type');
  assert.match(diags[0].message, /unbound reference 'x'/);
});

test('inner declarations shadow outer ones; duplicates in one block are errors', () => {
  resetIds();
  const outer = constDecl('a', intLit(1));
  const inner = constDecl('a', boolLit(true));
  const innerRef = ref('a');
  const innerBlock = block([inner, exprStmt(innerRef)]);
  const outerRef = ref('a');
  const dup1 = constDecl('d', intLit(1));
  const dup2 = constDecl('d', intLit(2));
  const refD = ref('d');
  const cp = new CheckedProgram(
    program(block([outer, innerBlock, exprStmt(outerRef), dup1, dup2, exprStmt(refD)])),
  );
  assert.equal(cp.bindOf(innerRef.id), inner.id);
  assert.equal(cp.bindOf(outerRef.id), outer.id);
  assert.equal(cp.typeOf(innerRef.id), 'bool');
  assert.equal(cp.typeOf(outerRef.id), 'int');
  const diags = cp.check();
  assert.equal(diags.length, 1);
  assert.equal(diags[0].nodeId, dup2.id);
  assert.equal(diags[0].attr, 'envOut');
  assert.match(diags[0].message, /duplicate declaration of 'd'/);
  // references still bind to the first declaration
  assert.equal(cp.bindOf(refD.id), dup1.id);
});

test('function parameters scope over the body; recursion is allowed', () => {
  resetIds();
  const refX = ref('x');
  const declC = constDecl('c', bin('+', refX, intLit(1)));
  const refF = ref('f');
  const body = block([declC, exprStmt(refF)]);
  const func = funcDecl('f', [['x', 'int'], ['y', 'bool']], 'int', body);
  const cp = new CheckedProgram(program(block([func])));
  assert.equal(cp.check().length, 0);
  assert.equal(cp.bindOf(refX.id), func.params[0].id);
  assert.equal(cp.bindOf(refF.id), func.id);
  assert.equal(cp.typeOf(func.id), '(int,bool)->int');
  assert.equal(cp.valueOf(declC.id), 'unknown'); // parameter is not a constant
  assert.equal(cp.typeOf(declC.id), 'int');
});

test('duplicate parameters are reported at the later parameter', () => {
  resetIds();
  const body = block([]);
  const func = funcDecl('f', [['x', 'int'], ['x', 'bool']], 'int', body);
  const cp = new CheckedProgram(program(block([func])));
  const diags = cp.check();
  assert.equal(diags.length, 1);
  assert.equal(diags[0].nodeId, func.params[1].id);
  assert.equal(diags[0].attr, 'envOut');
});

test('unary operators and boolean expressions', () => {
  resetIds();
  const declA = constDecl('a', un('-', intLit(5)));
  const declB = constDecl('b', bin('&&', boolLit(true), un('!', boolLit(false))));
  const declC = constDecl('c', bin('<', ref('a'), intLit(0)));
  const cp = new CheckedProgram(program(block([declA, declB, declC])));
  assert.equal(cp.check().length, 0);
  assert.equal(cp.valueOf(declA.id), -5);
  assert.equal(cp.valueOf(declB.id), true);
  assert.equal(cp.valueOf(declC.id), true);
  assert.equal(cp.typeOf(declC.id), 'bool');
});

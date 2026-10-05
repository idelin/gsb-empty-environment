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
  type Block,
  type ConstDecl,
  type ExprStmt,
  type FuncDecl,
  type Ref,
} from '../src/ast.ts';
import { deepEqual } from '../src/evaluator.ts';
import { CheckedProgram } from '../src/program.ts';
import { referenceCheck } from '../src/reference.ts';

// Builds:
//   const a = 1;
//   const b = a + 2;
//   func f(x: int, y: bool): int { const c = x + b; c; }
//   const d = b;
//   { const a = true; a; }
//   d;
function build() {
  resetIds();
  const declA = constDecl('a', intLit(1));
  const refAinB = ref('a');
  const declB = constDecl('b', bin('+', refAinB, intLit(2)));
  const refX = ref('x');
  const refBinC = ref('b');
  const declC = constDecl('c', bin('+', refX, refBinC));
  const stmtC = exprStmt(ref('c'));
  const bodyF = block([declC, stmtC]);
  const funcF = funcDecl('f', [['x', 'int'], ['y', 'bool']], 'int', bodyF);
  const refBinD = ref('b');
  const declD = constDecl('d', refBinD);
  const declAInner = constDecl('a', boolLit(true));
  const stmtAInner = exprStmt(ref('a'));
  const innerBlock = block([declAInner, stmtAInner]);
  const stmtDTrail = exprStmt(ref('d'));
  const body = block([declA, declB, funcF, declD, innerBlock, stmtDTrail]);
  const root = program(body);
  return {
    root, body, bodyF,
    declA, declB, declC, declD, declAInner,
    funcF, innerBlock, stmtC, stmtDTrail,
    refAinB, refX, refBinC, refBinD,
  };
}

function assertMatchesReference(cp: CheckedProgram): void {
  assert.ok(
    deepEqual(cp.snapshot(), referenceCheck(cp.root)),
    'incremental snapshot diverges from reference',
  );
}

test('moving a declaration across its reference breaks and restores binding', () => {
  const p = build();
  const cp = new CheckedProgram(p.root);
  assert.equal(cp.check().length, 0);
  assertMatchesReference(cp);

  // move "const a" after "const b": the reference in b's init is now unbound
  cp.apply({ op: 'move', stmtId: p.declA.id, targetBlockId: p.body.id, index: 2 });
  const diags = cp.check();
  assert.equal(diags.length, 1);
  assert.equal(diags[0].nodeId, p.refAinB.id);
  assert.equal(diags[0].attr, 'type');
  assert.match(diags[0].message, /unbound reference 'a'/);
  assert.equal(cp.bindOf(p.refAinB.id), null);
  assertMatchesReference(cp);

  // move it back: error disappears
  cp.apply({ op: 'move', stmtId: p.declA.id, targetBlockId: p.body.id, index: 0 });
  assert.equal(cp.check().length, 0);
  assert.equal(cp.bindOf(p.refAinB.id), p.declA.id);
  assertMatchesReference(cp);

  // node id and cached constant value survive the moves untouched
  assert.equal(p.declA.kind, 'const');
  assert.equal(cp.valueOf((p.declA as ConstDecl).init.id), 1);
});

test('inserting a shadowing declaration rebinds only the inner references', () => {
  const p = build();
  const cp = new CheckedProgram(p.root);
  cp.snapshot(); // force all attributes so cache versions exist
  const outerBindBefore = cp.bindOf(p.refBinD.id);
  const versionBefore = cp.ev.versionOf(`value@${(p.declA as ConstDecl).init.id}`);
  assert.equal(versionBefore, 1);

  const shadow = constDecl('b', intLit(10));
  cp.apply({ op: 'insert', blockId: p.bodyF.id, index: 0, stmt: shadow });

  assert.equal(cp.bindOf(p.refBinC.id), shadow.id); // inner ref rebinds
  assert.equal(cp.bindOf(p.refBinD.id), outerBindBefore); // outer ref untouched
  // unrelated cached attributes were not recomputed
  assert.equal(cp.ev.versionOf(`value@${(p.declA as ConstDecl).init.id}`), versionBefore);
  assert.equal(cp.check().length, 0);
  assertMatchesReference(cp);
});

test('deleting an outer definition keeps inner shadows and breaks outer references', () => {
  const p = build();
  const cp = new CheckedProgram(p.root);
  const shadow = constDecl('b', intLit(10));
  cp.apply({ op: 'insert', blockId: p.bodyF.id, index: 0, stmt: shadow });
  cp.check();

  cp.apply({ op: 'remove', stmtId: p.declB.id });
  const diags = cp.check();
  assert.equal(diags.length, 1);
  assert.equal(diags[0].nodeId, p.refBinD.id);
  assert.match(diags[0].message, /unbound reference 'b'/);
  assert.equal(cp.bindOf(p.refBinC.id), shadow.id); // inner shadow still works
  assertMatchesReference(cp);
});

test('renaming a parameter invalidates and repairs references to it', () => {
  const p = build();
  const cp = new CheckedProgram(p.root);
  cp.check();
  const paramX = (p.funcF as FuncDecl).params[0];

  cp.apply({ op: 'renameParam', paramId: paramX.id, newName: 'z' });
  const diags = cp.check();
  assert.equal(diags.length, 1);
  assert.equal(diags[0].nodeId, p.refX.id);
  assert.match(diags[0].message, /unbound reference 'x'/);
  assertMatchesReference(cp);

  cp.apply({ op: 'renameParam', paramId: paramX.id, newName: 'x' });
  assert.equal(cp.check().length, 0);
  assert.equal(cp.bindOf(p.refX.id), paramX.id);
  assertMatchesReference(cp);
});

test('batch edits equal sequential edits and the reference', () => {
  const edits = (ctx: ReturnType<typeof build>) => [
    {
      op: 'insert' as const,
      blockId: ctx.body.id,
      index: 6,
      stmt: constDecl('e', bin('+', ref('d'), intLit(1))),
    },
    { op: 'remove' as const, stmtId: (ctx.stmtDTrail as ExprStmt).id },
    { op: 'replaceExpr' as const, exprId: (ctx.declA as ConstDecl).init.id, expr: intLit(41) },
  ];

  const p1 = build();
  const cp1 = new CheckedProgram(p1.root);
  cp1.check();
  cp1.applyAll(edits(p1));

  const p2 = build();
  const cp2 = new CheckedProgram(p2.root);
  cp2.check();
  for (const e of edits(p2)) cp2.apply(e);

  assert.ok(deepEqual(cp1.snapshot(), cp2.snapshot()), 'batch != sequential');
  assertMatchesReference(cp1);
  assert.equal(cp1.valueOf((p1.declA as ConstDecl).id), 41);
});

test('only affected attributes are recomputed after an edit', () => {
  const p = build();
  const cp = new CheckedProgram(p.root);
  cp.snapshot(); // force every attribute once
  const totalSlots = cp.ev.keys().length;

  // small, local edit: insert a shadowing decl inside the function body
  const shadow = constDecl('b', intLit(10));
  cp.apply({ op: 'insert', blockId: p.bodyF.id, index: 0, stmt: shadow });
  cp.snapshot(); // pull all attributes again after the edit
  const s = cp.ev.stats;
  assert.ok(s.recomputed > 0, 'something must be recomputed');
  assert.ok(
    s.recomputed < totalSlots / 2,
    `recomputed ${s.recomputed} of ${totalSlots} slots; expected a small fraction`,
  );
  // attributes of the outer "const a = 1" were not touched at all
  assert.equal(cp.ev.versionOf(`value@${(p.declA as ConstDecl).init.id}`), 1);
  assert.equal(cp.ev.versionOf(`type@${(p.declA as ConstDecl).init.id}`), 1);
});

// Replayable edit script. Run with: node examples/replay.ts
// Every step applies edits to the incremental evaluator, prints how
// many attribute instances were invalidated / recomputed / verified
// clean, and checks the full snapshot against the from-scratch
// reference implementation.

import assert from 'node:assert';
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
  type Diagnostic,
} from '../src/ast.ts';
import { deepEqual } from '../src/evaluator.ts';
import { CheckedProgram, type Edit } from '../src/program.ts';
import { referenceCheck } from '../src/reference.ts';

function fmtDiags(diags: Diagnostic[]): string {
  if (diags.length === 0) return '(none)';
  return diags.map((d) => `${d.nodeId}.${d.attr}: ${d.message}`).join('; ');
}

export function runReplay(): string {
  const out: string[] = [];
  const log = (s = ''): void => {
    out.push(s);
  };

  resetIds();
  // const a = 1;
  // const b = a + 2;
  // func f(x: int, y: bool): int { const c = x + b; c; }
  // const d = b;
  // { const a = true; a; }
  // d;
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

  const cp = new CheckedProgram(root);
  const paramX = funcF.params[0];

  const step = (title: string, edits: Edit[]): void => {
    log(`== ${title}`);
    for (const e of edits) log(`   edit: ${JSON.stringify(e, (k, v) => (k === 'stmt' || k === 'expr' ? `<${v.kind} ${v.id}>` : v))}`);
    cp.applyAll(edits);
    const diags = cp.check();
    const s = cp.ev.stats;
    log(`   invalidated=${s.invalidated} recomputed=${s.recomputed} verified-clean=${s.verified}`);
    log(`   diagnostics: ${fmtDiags(diags)}`);
    const match = deepEqual(cp.snapshot(), referenceCheck(root));
    log(`   reference match: ${match ? 'OK' : 'MISMATCH'}`);
    assert.ok(match, `incremental result diverges from reference after: ${title}`);
    log('');
  };

  log('== step 0: initial check');
  const diags0 = cp.check();
  log(`   diagnostics: ${fmtDiags(diags0)}`);
  log(`   b = ${cp.valueOf(declB.id)}, d = ${cp.valueOf(declD.id)}, c = ${cp.valueOf(declC.id)} (x is a parameter)`);
  log(`   dependency graph below value@${refBinD.id} (the reference in "const d = b"):`);
  log(cp.ev.explain(`value@${refBinD.id}`, 6).replace(/^/gm, '   '));
  const pathValue = cp.ev.depPath(`value@${refBinD.id}`, `value@${declA.id}`);
  log(`   concrete value path: ${pathValue === null ? 'none' : pathValue.join(' -> ')}`);
  const pathScope = cp.ev.depPath(`bind@${refAinB.id}`, `scopeBase@${body.id}`);
  log(`   concrete scope path: ${pathScope === null ? 'none' : pathScope.join(' -> ')}`);
  log('   note: envOut@decl depends on cell/env only, never on value@decl,');
  log('   so the inherited<->synthesized chain above cannot close a cycle.');
  const match0 = deepEqual(cp.snapshot(), referenceCheck(root));
  log(`   reference match: ${match0 ? 'OK' : 'MISMATCH'}`);
  assert.ok(match0);
  log('');

  step('step 1: move "const a" after "const b" (declaration crosses its reference)', [
    { op: 'move', stmtId: declA.id, targetBlockId: body.id, index: 2 },
  ]);

  step('step 2: move "const a" back to the top', [
    { op: 'move', stmtId: declA.id, targetBlockId: body.id, index: 0 },
  ]);

  step('step 3: insert shadowing "const b = 10" at the top of f\'s body', [
    { op: 'insert', blockId: bodyF.id, index: 0, stmt: constDecl('b', intLit(10)) },
  ]);
  log(`   bind of b-reference inside f: ${cp.bindOf(refBinC.id)} (shadows outer ${declB.id})`);
  log('');

  step('step 4: delete the outer "const b"', [{ op: 'remove', stmtId: declB.id }]);
  log(`   inner shadow still binds: ${cp.bindOf(refBinC.id) !== null}`);
  log('');

  step('step 5: insert "const b = 100" before "const d" (repairs the unbound reference)', [
    { op: 'insert', blockId: body.id, index: 2, stmt: constDecl('b', intLit(100)) },
  ]);
  log(`   d = ${cp.valueOf(declD.id)}`);
  log('');

  step('step 6: rename parameter x -> z (reference to x becomes unbound)', [
    { op: 'renameParam', paramId: paramX.id, newName: 'z' },
  ]);

  step('step 7: rename parameter z -> x (reference repaired)', [
    { op: 'renameParam', paramId: paramX.id, newName: 'x' },
  ]);

  step('step 8: batch edit: append "const e = d + 1" and remove the trailing "d;"', [
    { op: 'insert', blockId: body.id, index: 6, stmt: constDecl('e', bin('+', ref('d'), intLit(1))) },
    { op: 'remove', stmtId: stmtDTrail.id },
  ]);
  log(`   e = ${cp.valueOf(body.stmts[body.stmts.length - 1].id)}`);
  log('');

  log('all steps match the from-scratch reference implementation');
  return out.join('\n');
}

if (import.meta.url === `file://${process.argv[1]}`) {
  process.stdout.write(`${runReplay()}\n`);
}

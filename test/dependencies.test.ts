import test from 'node:test';
import assert from 'node:assert/strict';
import {
  bin,
  block,
  constDecl,
  exprStmt,
  funcDecl,
  intLit,
  program,
  ref,
  resetIds,
  type ConstDecl,
} from '../src/ast.ts';
import { CycleError, Evaluator } from '../src/evaluator.ts';
import { CheckedProgram } from '../src/program.ts';
import { parseKey } from '../src/semantics.ts';

function build() {
  resetIds();
  // const a = 1;
  // const b = a + 2;
  // func f(x: int): int { const c = x + b; c; }
  const declA = constDecl('a', intLit(1));
  const refA = ref('a');
  const declB = constDecl('b', bin('+', refA, intLit(2)));
  const refX = ref('x');
  const refB = ref('b');
  const declC = constDecl('c', bin('+', refX, refB));
  const bodyF = block([declC, exprStmt(ref('c'))]);
  const funcF = funcDecl('f', [['x', 'int']], 'int', bodyF);
  const body = block([declA, declB, funcF]);
  const root = program(body);
  return { root, body, bodyF, declA, declB, declC, funcF, refA, refX, refB };
}

test('dependency edges connect inherited and synthesized attributes explicitly', () => {
  const p = build();
  const cp = new CheckedProgram(p.root);
  cp.snapshot(); // force every attribute so all edges are recorded

  // inherited env of a statement depends on the synthesized envOut of
  // the previous statement
  assert.ok(cp.ev.depsOf(`env@${p.declB.id}`).includes(`envOut@${p.declA.id}`));
  // synthesized envOut of a declaration depends on the inherited env
  // and on the scope cell of its own name
  const envOutDeps = cp.ev.depsOf(`envOut@${p.declB.id}`);
  assert.ok(envOutDeps.includes(`env@${p.declB.id}`));
  assert.ok(envOutDeps.includes(`cell@${p.body.id}:b`));
  // name binding depends on the inherited env and on scope cells
  const bindDeps = cp.ev.depsOf(`bind@${p.refB.id}`);
  assert.ok(bindDeps.some((k) => k.startsWith('env@')));
  assert.ok(bindDeps.includes(`cell@${p.bodyF.id}:b`));
  assert.ok(bindDeps.includes(`cell@${p.body.id}:b`));
  // constant values chain across declarations
  assert.ok(cp.ev.depsOf(`value@${p.declB.id}`).includes(`value@${(p.declB as ConstDecl).init.id}`));
  // every edge is also present in the explicit dependency log
  assert.ok(
    cp.ev.depLog.some((e) => e.observer === `env@${p.declB.id}` && e.dependency === `envOut@${p.declA.id}`),
  );
});

test('an unbound reference records dependencies on every enclosing scope', () => {
  resetIds();
  const refY = ref('y');
  const innerBlock = block([exprStmt(refY)]);
  const funcF = funcDecl('f', [['x', 'int']], 'int', innerBlock);
  const body = block([funcF]);
  const cp = new CheckedProgram(program(body));
  const diags = cp.check();
  assert.equal(diags.length, 1);
  assert.match(diags[0].message, /unbound reference 'y'/);

  const deps = cp.ev.depsOf(`bind@${refY.id}`);
  assert.ok(deps.includes(`cell@${innerBlock.id}:y`), 'depends on the function body scope');
  assert.ok(deps.includes(`cell@${body.id}:y`), 'depends on the outer block scope');

  // inserting the declaration later (before the function, so it is
  // visible inside the body) repairs the error
  const versionBefore = cp.ev.versionOf(`bind@${refY.id}`);
  const declY = constDecl('y', intLit(7));
  cp.apply({ op: 'insert', blockId: body.id, index: 0, stmt: declY });
  assert.equal(cp.check().length, 0);
  assert.equal(cp.bindOf(refY.id), declY.id);
  assert.ok((cp.ev.versionOf(`bind@${refY.id}`) as number) > (versionBefore as number));
});

test('cache keys are node-id based, never bare variable names', () => {
  resetIds();
  // two references to the same name in different scopes bind differently
  const declA = constDecl('a', intLit(1));
  const innerDecl = constDecl('a', intLit(2));
  const refInner = ref('a');
  const refOuter = ref('a');
  const innerBlock = block([innerDecl, exprStmt(refInner)]);
  const body = block([declA, innerBlock, exprStmt(refOuter)]);
  const cp = new CheckedProgram(program(body));
  cp.check();
  assert.equal(cp.bindOf(refInner.id), innerDecl.id);
  assert.equal(cp.bindOf(refOuter.id), declA.id);
  // attribute keys embed the node id
  assert.equal(parseKey(`bind@${refInner.id}`).nodeId, refInner.id);
  assert.notEqual(refInner.id, refOuter.id);
  // scope cell keys embed the block id, not just the variable name
  const cellKeys = cp.ev.keys().filter((k) => k.startsWith('cell@'));
  assert.ok(cellKeys.includes(`cell@${body.id}:a`));
  assert.ok(cellKeys.includes(`cell@${innerBlock.id}:a`));
  const parsed = parseKey(`cell@${innerBlock.id}:a`);
  assert.equal(parsed.nodeId, innerBlock.id);
  assert.equal(parsed.name, 'a');
});

test('node ids are stable across moves and cache entries survive', () => {
  const p = build();
  const cp = new CheckedProgram(p.root);
  cp.snapshot(); // force all attributes, not only the error attribute
  const initId = (p.declA as ConstDecl).init.id;
  const versionBefore = cp.ev.versionOf(`value@${initId}`);
  assert.equal(versionBefore, 1);

  cp.apply({ op: 'move', stmtId: p.declA.id, targetBlockId: p.body.id, index: 2 });
  // the node kept its id, and its position-insensitive constant value
  // was not recomputed (same cache entry, same version)
  assert.equal((p.declA as ConstDecl).init.id, initId);
  assert.equal(cp.valueOf(initId), 1);
  assert.equal(cp.ev.versionOf(`value@${initId}`), versionBefore);
});

test('cross-attribute cycles are detected with the concrete dependency path', () => {
  // The language itself is acyclic by construction: envOut@decl depends
  // on cell/env (the declared *name*) but never on value@decl, so a
  // chain like value@decl -> value@init -> bind@ref -> env@ref ->
  // envOut@decl can never loop back into value@decl. The evaluator
  // still guards against cycles; feed it a cyclic attribute program:
  const ev = new Evaluator((key) => () => ev.get(key === 'a' ? 'b' : 'a'));
  assert.throws(() => ev.get('a'), (err: unknown) => {
    assert.ok(err instanceof CycleError);
    assert.deepEqual((err as CycleError).path, ['a', 'b', 'a']);
    assert.match((err as Error).message, /cyclic attribute dependency: a -> b -> a/);
    return true;
  });
});

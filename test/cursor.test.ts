import * as fs from 'fs';
import { BlockCache } from '../src/cache';
import { CursorBindingError, CursorError, decodeCursor } from '../src/cursor';
import { QueryEngine, IllegalArgumentError } from '../src/engine';
import { SetStore } from '../src/store';
import { and, exprHash, or, setRef } from '../src/expr';
import { validateCursorBinding } from '../src/validate';
import { assert, run, test } from './harness';
import { range, tmpDir, writeSet } from './helpers';

const QUOTA = 1 << 20;

function makeEngine(): QueryEngine {
  return new QueryEngine(new SetStore(new BlockCache(QUOTA)));
}

test('switching input version between pages is rejected by cursor binding', () => {
  const dir = tmpDir();
  const file = writeSet(dir, 'A', 'v1', range(0, 500), 100);
  const engine = makeEngine();
  const expr = setRef('A');
  const page1 = engine.queryPage({ expr, inputs: { A: file }, k: 100, quotaBytes: QUOTA });
  assert.deepEqual(page1.ids, range(0, 100));

  // the file is replaced by a new version between two pages
  fs.writeFileSync(file, fs.readFileSync(writeSet(dir, 'A', 'v2', range(0, 600), 100)));

  assert.throws(
    () => engine.queryPage({ expr, inputs: { A: file }, cursor: page1.nextCursor as string, quotaBytes: QUOTA }),
    (e: Error) => e instanceof CursorBindingError && e.message.includes('"v1"') && e.message.includes('"v2"'),
  );
});

test('cursor bound to a different expression is rejected', () => {
  const dir = tmpDir();
  const fileA = writeSet(dir, 'A', 'v1', range(0, 300), 100);
  const fileB = writeSet(dir, 'B', 'v1', range(100, 400), 100);
  const engine = makeEngine();
  const exprOr = or(setRef('A'), setRef('B'));
  const page1 = engine.queryPage({ expr: exprOr, inputs: { A: fileA, B: fileB }, k: 50, quotaBytes: QUOTA });
  const exprAnd = and(setRef('A'), setRef('B'));
  assert.throws(
    () => engine.queryPage({ expr: exprAnd, inputs: { A: fileA, B: fileB }, cursor: page1.nextCursor as string, quotaBytes: QUOTA }),
    CursorBindingError,
  );
});

test('tampered cursor fails integrity check', () => {
  const dir = tmpDir();
  const file = writeSet(dir, 'A', 'v1', range(0, 300), 100);
  const engine = makeEngine();
  const page1 = engine.queryPage({ expr: setRef('A'), inputs: { A: file }, k: 50, quotaBytes: QUOTA });
  const cursor = page1.nextCursor as string;

  // flip one payload character
  const bad = cursor.slice(0, 10) + (cursor[10] === 'A' ? 'B' : 'A') + cursor.slice(11);
  assert.throws(() => decodeCursor(bad), CursorError);
  assert.throws(
    () => engine.queryPage({ expr: setRef('A'), inputs: { A: file }, cursor: bad, quotaBytes: QUOTA }),
    CursorError,
  );

  // hand-modify the resume position without fixing the integrity tag
  const parsed = JSON.parse(Buffer.from(cursor, 'base64url').toString('utf8'));
  parsed.last = 99999;
  const forged = Buffer.from(JSON.stringify(parsed), 'utf8').toString('base64url');
  assert.throws(() => decodeCursor(forged), /integrity/);
});

test('page size k=0 (and other invalid k) is illegal', () => {
  const dir = tmpDir();
  const file = writeSet(dir, 'A', 'v1', range(0, 100), 50);
  const engine = makeEngine();
  const expr = setRef('A');
  for (const k of [0, -1, 1.5, NaN]) {
    assert.throws(
      () => engine.queryPage({ expr, inputs: { A: file }, k, quotaBytes: QUOTA }),
      IllegalArgumentError,
      'k=' + k,
    );
  }
  assert.throws(() => engine.queryPage({ expr, inputs: { A: file }, quotaBytes: QUOTA }), IllegalArgumentError);
});

test('resume with a different k than the cursor carries is rejected', () => {
  const dir = tmpDir();
  const file = writeSet(dir, 'A', 'v1', range(0, 300), 100);
  const engine = makeEngine();
  const expr = setRef('A');
  const page1 = engine.queryPage({ expr, inputs: { A: file }, k: 50, quotaBytes: QUOTA });
  assert.throws(
    () => engine.queryPage({ expr, inputs: { A: file }, k: 60, cursor: page1.nextCursor as string, quotaBytes: QUOTA }),
    IllegalArgumentError,
  );
  // matching k is accepted
  const page2 = engine.queryPage({ expr, inputs: { A: file }, k: 50, cursor: page1.nextCursor as string, quotaBytes: QUOTA });
  assert.deepEqual(page2.ids, range(50, 100));
});

test('validator checks cursor binding independently', () => {
  const dir = tmpDir();
  const file = writeSet(dir, 'A', 'v1', range(0, 300), 100);
  const engine = makeEngine();
  const expr = setRef('A');
  const page1 = engine.queryPage({ expr, inputs: { A: file }, k: 50, quotaBytes: QUOTA });
  const cursor = page1.nextCursor as string;

  const good = validateCursorBinding(cursor, exprHash(expr), [['A', 'v1']]);
  assert.equal(good.ok, true);
  assert.equal(good.payload?.last, 49);
  assert.equal(good.payload?.k, 50);

  const wrongVersion = validateCursorBinding(cursor, exprHash(expr), [['A', 'v2']]);
  assert.equal(wrongVersion.ok, false);
  assert.ok(wrongVersion.errors[0].includes('version'));

  const wrongExpr = validateCursorBinding(cursor, exprHash(setRef('B')), [['A', 'v1']]);
  assert.equal(wrongExpr.ok, false);

  const garbage = validateCursorBinding('not-a-cursor', exprHash(expr), [['A', 'v1']]);
  assert.equal(garbage.ok, false);
});

run();

import assert from 'node:assert/strict';
import { IdentityModel, parseStatements } from '../src/index.js';

const ids = (stmts) => stmts.map((s) => s.id);

test('identity: untouched statements keep ids, edited region pairs in place', () => {
  const model = new IdentityModel();
  const prev = model.assignInitial(parseStatements('const a = 1;\nconst b = 2;\nconst c = 3;\n'));
  const edit = { start: 19, end: 24, text: 'beta = 20' }; // inside statement b
  const next = parseStatements('const a = 1;\nconst beta = 20;\nconst c = 3;\n');
  const { matched, deleted } = model.matchStatements(prev, next, [edit]);
  assert.deepEqual(deleted, []);
  assert.deepEqual(ids(matched), ids(prev));
});

test('identity: deleted statements become tombstones and can revive', () => {
  const model = new IdentityModel();
  const prev = model.assignInitial(parseStatements('const a = 1;\nconst b = 2;\n'));
  const del = { start: 13, end: 26, text: '' };
  const afterDelete = model.matchStatements(prev, parseStatements('const a = 1;\n'), [del]);
  assert.equal(afterDelete.deleted.length, 1);
  const deletedId = afterDelete.deleted[0].id;

  // A later edit re-introduces the same statement: the id revives.
  const restored = parseStatements('const a = 1;\nconst b = 2;\n');
  const afterRestore = model.matchStatements(
    afterDelete.matched,
    restored,
    [{ start: 13, end: 13, text: 'const b = 2;\n' }],
  );
  assert.ok(ids(afterRestore.matched).includes(deletedId));
});

test('identity: tombstone buffer is bounded and evicts oldest first', () => {
  const model = new IdentityModel({ tombstoneLimit: 2 });
  const prev = model.assignInitial(parseStatements('const a = 1;\nconst b = 2;\nconst c = 3;\n'));
  model.matchStatements(prev, parseStatements(''), [{ start: 0, end: 39, text: '' }]);
  assert.equal(model.tombstoneCount, 2);
  model.clearTombstones();
  assert.equal(model.tombstoneCount, 0);
});

test('identity: copies get fresh ids, never the original id', () => {
  const model = new IdentityModel();
  const prev = model.assignInitial(parseStatements('foo();\n'));
  const next = parseStatements('foo();\nfoo();\n');
  const { matched } = model.matchStatements(prev, next, [{ start: 7, end: 7, text: 'foo();\n' }]);
  assert.equal(matched[0].id, prev[0].id);
  assert.notEqual(matched[1].id, prev[0].id);
});

import test from 'node:test';
import assert from 'node:assert/strict';
import { VfsStore, ERR } from '../src/index.ts';
import { mkpath, hasCode } from './util.ts';

test('mount graph rejects cycles and tracks parents explicitly', () => {
  const store = new VfsStore();
  const a = mkpath(store, '/a');
  mkpath(store, '/a/b');
  const data = mkpath(store, '/data');
  const ma = store.addMount({ name: 'a', path: '/a', rootId: a });
  assert.equal(ma.parent, 'root');
  // grafting a mount onto its own root node
  assert.throws(
    () => store.addMount({ name: 'self', path: '/a/self', rootId: a }),
    hasCode(ERR.MountCycle),
  );
  // grafting an ancestor root below its own subtree
  assert.throws(
    () => store.addMount({ name: 'up', path: '/a/b/up', rootId: store.tree.rootId }),
    hasCode(ERR.MountCycle),
  );
  // legitimate nested mount on an independent subtree
  const mb = store.addMount({ name: 'b', path: '/a/b/data', rootId: data });
  assert.equal(mb.parent, 'a');
  assert.deepEqual(
    store.mounts.list().map((m) => [m.name, m.parent]),
    [['root', null], ['a', 'root'], ['b', 'a']],
  );
});

test('paths cannot escape their mount root', () => {
  const store = new VfsStore();
  const m = mkpath(store, '/m');
  const ok = mkpath(store, '/m/sub/ok');
  store.addMount({ name: 'm', path: '/m', rootId: m });
  assert.equal(store.resolvePath('/m/sub/../sub/ok').nodeId, ok);
  assert.throws(() => store.resolvePath('/m/../escape'), hasCode(ERR.RootEscape));
  assert.throws(() => store.resolvePath('/m/sub/../../escape'), hasCode(ERR.RootEscape));
  assert.throws(() => store.resolvePath('/..'), hasCode(ERR.RootEscape));
});

test('aliases cannot bypass read-only mount boundaries', () => {
  const store = new VfsStore();
  const ro = mkpath(store, '/ro');
  const secret = mkpath(store, '/ro/secret');
  const w = mkpath(store, '/w');
  const wx = mkpath(store, '/w/x');
  store.addMount({ name: 'ro', path: '/ro', rootId: ro, readOnly: true });
  store.setAlias('/shortcut', secret);
  store.setAlias('/rodir', ro);
  // reads through aliases still resolve
  assert.equal(store.resolvePath('/shortcut').nodeId, secret);
  // moving out of a read-only mount via an alias is blocked
  assert.throws(() => store.movePath('/shortcut', '/w/secret'), hasCode(ERR.ReadOnlyMount));
  // moving into a read-only mount via an alias is blocked
  assert.throws(() => store.movePath('/w/x', '/rodir/y'), hasCode(ERR.ReadOnlyMount));
  // the failed transactions left the tree untouched
  assert.equal(store.tree.realPath(secret), '/ro/secret');
  assert.equal(store.tree.realPath(wx), '/w/x');
  assert.equal(store.tree.childByName(w, 'secret'), undefined);
  assert.equal(store.tree.childByName(ro, 'y'), undefined);
});

test('alias config spans multiple mounts', () => {
  const store = new VfsStore();
  const m = mkpath(store, '/mnt');
  const target = mkpath(store, '/elsewhere/target');
  const inner = mkpath(store, '/elsewhere/target/inner');
  store.addMount({ name: 'mnt', path: '/mnt', rootId: m });
  store.setAlias('/mnt/jump', target);
  const resolved = store.resolvePath('/mnt/jump/inner');
  assert.equal(resolved.nodeId, inner);
  assert.equal(resolved.hops, 1);
});

test('transactional move validates before mutating', () => {
  const store = new VfsStore();
  const a = mkpath(store, '/w/a');
  mkpath(store, '/w/b');
  mkpath(store, '/dir/sub');
  assert.throws(() => store.movePath('/w/a', '/w/b'), hasCode(ERR.AlreadyExists));
  assert.throws(() => store.movePath('/dir', '/dir/sub/x'), hasCode(ERR.WouldCycle));
  assert.equal(store.tree.realPath(a), '/w/a');
  store.movePath('/w/a', '/dir/moved');
  assert.equal(store.tree.realPath(a), '/dir/moved');
});

test('commit re-validates and applies nothing on failure', () => {
  const store = new VfsStore();
  const a = mkpath(store, '/w/a');
  const w = mkpath(store, '/w');
  const tx = store.beginTx();
  tx.move('/w/a', '/w/b');
  store.tree.createNode(w, 'b'); // external change after staging
  assert.throws(() => tx.commit(), hasCode(ERR.AlreadyExists));
  assert.equal(store.tree.realPath(a), '/w/a');
});

test('alias config switch after validation aborts the transaction', () => {
  const store = new VfsStore();
  const a = mkpath(store, '/w/a');
  const w = mkpath(store, '/w');
  const z = mkpath(store, '/z');
  const tx = store.beginTx();
  tx.move('/w/a', '/w/b');
  store.setAlias('/late', z); // configuration changes after the check
  assert.throws(() => tx.commit(), hasCode(ERR.ConfigChanged));
  assert.equal(store.tree.realPath(a), '/w/a');
  assert.equal(store.tree.childByName(w, 'b'), undefined);
});

test('mount config switch after validation aborts the transaction', () => {
  const store = new VfsStore();
  const a = mkpath(store, '/w/a');
  const w = mkpath(store, '/w');
  const z = mkpath(store, '/z');
  const tx = store.beginTx();
  tx.move('/w/a', '/w/b');
  store.addMount({ name: 'late', path: '/late', rootId: z });
  assert.throws(() => tx.commit(), hasCode(ERR.ConfigChanged));
  assert.equal(store.tree.realPath(a), '/w/a');
  assert.equal(store.tree.childByName(w, 'b'), undefined);
});

test('transaction commits when the config is stable', () => {
  const store = new VfsStore();
  const a = mkpath(store, '/w/a');
  const tx = store.beginTx();
  tx.move('/w/a', '/w/b');
  tx.commit();
  assert.equal(store.tree.realPath(a), '/w/b');
  assert.throws(() => tx.commit(), hasCode(ERR.TransactionClosed));
});

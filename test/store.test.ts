import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Store, keyOf, serializeState } from '../src/index.ts';

const AUTHOR = keyOf('Author', 1);
const BOOK1 = keyOf('Book', 1);
const BOOK2 = keyOf('Book', 2);

function seedStore(): Store {
  const store = new Store();
  const tx = store.begin();
  tx.applyResponse({
    __typename: 'Book',
    id: 1,
    title: 'Dune',
    author: { __typename: 'Author', id: 1, name: 'Herbert' },
    reviewers: [
      { __typename: 'User', id: 1, handle: 'ada' },
      { __typename: 'User', id: 2, handle: 'grace' },
    ],
  });
  tx.applyResponse({
    __typename: 'Book',
    id: 2,
    title: 'Dune Messiah',
    author: { __typename: 'Author', id: 1, name: 'Herbert' },
  });
  tx.commit();
  return store;
}

test('嵌套响应按类型+ID 拆成实体表，引用共享', () => {
  const store = seedStore();
  assert.deepEqual(store.entityKeys(), [AUTHOR, BOOK1, BOOK2, 'User:1', 'User:2']);
  // nested entity replaced by a shared ref
  assert.deepEqual(store.getField(BOOK1, 'author'), { $ref: AUTHOR });
  assert.deepEqual(store.getField(BOOK2, 'author'), { $ref: AUTHOR });
  assert.deepEqual(store.getField(BOOK1, 'reviewers'), [{ $ref: 'User:1' }, { $ref: 'User:2' }]);
  // reverse index tracks both parents
  assert.deepEqual(store.inboundRefs(AUTHOR), { [BOOK1]: ['author'], [BOOK2]: ['author'] });
});

test('两个父引用同一实体时同步看到更新', () => {
  const store = seedStore();
  const tx = store.begin();
  tx.writeField(AUTHOR, 'name', 'Frank Herbert');
  tx.commit();
  const book1 = store.readEntity(BOOK1) as { author: { name: string } };
  const book2 = store.readEntity(BOOK2) as { author: { name: string } };
  assert.equal(book1.author.name, 'Frank Herbert');
  assert.equal(book2.author.name, 'Frank Herbert');
});

test('部分字段更新不删除未返回字段', () => {
  const store = seedStore();
  const tx = store.begin();
  tx.applyResponse({ __typename: 'Book', id: 1, title: 'Dune (revised)' });
  tx.commit();
  assert.equal(store.getField(BOOK1, 'title'), 'Dune (revised)');
  assert.deepEqual(store.getField(BOOK1, 'author'), { $ref: AUTHOR }); // untouched
  assert.ok(store.hasField(BOOK1, 'reviewers')); // untouched
});

test('缺字段与显式 null 严格区分', () => {
  const store = seedStore();
  const tx = store.begin();
  tx.applyResponse({ __typename: 'Book', id: 1, subtitle: null });
  tx.commit();
  assert.ok(store.hasField(BOOK1, 'subtitle'));
  assert.equal(store.getField(BOOK1, 'subtitle'), null);
  assert.ok(!store.hasField(BOOK1, 'isbn'));
  assert.equal(store.getField(BOOK1, 'isbn'), undefined);
});

test('显式删除清理反向索引并把引用置空', () => {
  const store = seedStore();
  const tx = store.begin();
  tx.deleteEntity(AUTHOR);
  tx.commit();
  assert.ok(!store.hasEntity(AUTHOR));
  assert.deepEqual(store.inboundRefs(AUTHOR), {});
  assert.equal(store.getField(BOOK1, 'author'), null);
  assert.equal(store.getField(BOOK2, 'author'), null);
  // list references are filtered, not nulled wholesale
  const tx2 = store.begin();
  tx2.deleteEntity('User:1');
  tx2.commit();
  assert.deepEqual(store.getField(BOOK1, 'reviewers'), [{ $ref: 'User:2' }]);
});

test('删除引用环中的实体不留下悬挂反向索引', () => {
  const store = new Store();
  const tx = store.begin();
  tx.applyResponse({ __typename: 'Node', id: 'a', next: { __typename: 'Node', id: 'b' } });
  tx.applyResponse({ __typename: 'Node', id: 'b', next: { __typename: 'Node', id: 'a' } });
  tx.commit();
  assert.deepEqual(store.inboundRefs('Node:a'), { 'Node:b': ['next'] });
  const tx2 = store.begin();
  tx2.deleteEntities(['Node:a', 'Node:b']);
  tx2.commit();
  assert.ok(!store.hasEntity('Node:a'));
  assert.ok(!store.hasEntity('Node:b'));
  assert.deepEqual(store.inboundRefs('Node:a'), {});
  assert.deepEqual(store.inboundRefs('Node:b'), {});
});

test('存活查询租约延迟删除，释放后才真正清理', () => {
  const store = seedStore();
  const lease = store.acquireLease([AUTHOR]);
  const tx = store.begin();
  tx.deleteEntity(AUTHOR);
  tx.commit();
  // leased: still readable, still referenced
  assert.ok(store.hasEntity(AUTHOR));
  assert.deepEqual(store.getField(BOOK1, 'author'), { $ref: AUTHOR });
  lease.release();
  assert.ok(!store.hasEntity(AUTHOR));
  assert.equal(store.getField(BOOK1, 'author'), null);
  assert.deepEqual(store.inboundRefs(AUTHOR), {});
});

test('分页关系合并：去重、保序、更新 pageInfo', () => {
  const store = new Store();
  const tx = store.begin();
  tx.applyResponse({ __typename: 'Book', id: 1, title: 'Dune' });
  tx.applyPage(BOOK1, 'chapters', [{ __typename: 'Chapter', id: 1 }, { __typename: 'Chapter', id: 2 }], {
    endCursor: 'c2',
    hasNextPage: true,
  });
  tx.commit();
  const tx2 = store.begin();
  tx2.applyPage(BOOK1, 'chapters', [{ __typename: 'Chapter', id: 2 }, { __typename: 'Chapter', id: 3 }], {
    endCursor: 'c3',
    hasNextPage: false,
  });
  tx2.commit();
  const value = store.getField(BOOK1, 'chapters') as {
    kind: string;
    refs: { $ref: string }[];
    pageInfo: { endCursor: string | null; hasNextPage: boolean };
  };
  assert.equal(value.kind, 'connection');
  assert.deepEqual(value.refs, [{ $ref: 'Chapter:1' }, { $ref: 'Chapter:2' }, { $ref: 'Chapter:3' }]);
  assert.deepEqual(value.pageInfo, { endCursor: 'c3', hasNextPage: false });
  const read = store.readEntity(BOOK1) as { chapters: { nodes: unknown[] } };
  assert.equal(read.chapters.nodes.length, 3);
});

test('乐观叠加层：立即可读、提交持久化、回滚只撤销自身', () => {
  const store = seedStore();
  const layer = store.optimistic();
  layer.writeField(BOOK1, 'title', 'Draft title');
  // overlay is visible to readers before commit
  assert.equal(store.getField(BOOK1, 'title'), 'Draft title');
  // committed snapshot is untouched
  assert.equal(store.snapshot().entities.get(BOOK1)!.fields.get('title')!.value, 'Dune');
  layer.rollback();
  assert.equal(store.getField(BOOK1, 'title'), 'Dune');

  const layer2 = store.optimistic();
  layer2.writeField(BOOK1, 'title', 'Draft v2');
  layer2.commit();
  assert.equal(store.getField(BOOK1, 'title'), 'Draft v2');
  assert.equal(store.snapshot().entities.get(BOOK1)!.fields.get('title')!.value, 'Draft v2');
});

test('旧服务器回包不能覆盖用户随后提交的字段版本', () => {
  const store = new Store();
  const tx = store.begin();
  tx.applyResponse({ __typename: 'Post', id: 1, title: 'server-v1' });
  tx.commit();
  const issuedAt = store.clock; // stale query was issued here

  const layer = store.optimistic();
  layer.writeField('Post:1', 'title', 'user-edit');
  layer.commit(); // user change commits at a newer version

  const tx2 = store.begin();
  tx2.applyResponse({ __typename: 'Post', id: 1, title: 'stale-server' }, { baseVersion: issuedAt });
  tx2.commit();
  assert.equal(store.getField('Post:1', 'title'), 'user-edit'); // stale write rejected

  const tx3 = store.begin();
  tx3.applyResponse({ __typename: 'Post', id: 1, title: 'fresh-server' }, { baseVersion: store.clock });
  tx3.commit();
  assert.equal(store.getField('Post:1', 'title'), 'fresh-server'); // fresh write accepted
});

test('身份合并原子更新反向索引并转移租约', () => {
  const store = seedStore();
  const tx0 = store.begin();
  tx0.applyResponse({ __typename: 'Author', id: 2, name: 'F. Herbert', email: 'f@dune.example' });
  tx0.commit();
  const tx1 = store.begin();
  tx1.writeField(BOOK2, 'author', { $ref: 'Author:2' });
  tx1.commit();
  const lease = store.acquireLease(['Author:2']);

  const tx = store.begin();
  tx.mergeIdentity('Author', 2, 1); // absorb Author:2 into Author:1
  tx.commit();

  assert.ok(!store.hasEntity('Author:2'));
  assert.deepEqual(store.getField(BOOK2, 'author'), { $ref: AUTHOR });
  assert.deepEqual(store.inboundRefs(AUTHOR), { [BOOK1]: ['author'], [BOOK2]: ['author'] });
  assert.deepEqual(store.inboundRefs('Author:2'), {});
  // fields merged: email came only from Author:2
  assert.equal(store.getField(AUTHOR, 'email'), 'f@dune.example');
  // conflict resolved by field version: Author:2's name was written later
  assert.equal(store.getField(AUTHOR, 'name'), 'F. Herbert');
  // lease transferred: deleting Author:1 is deferred until release
  const tx2 = store.begin();
  tx2.deleteEntity(AUTHOR);
  tx2.commit();
  assert.ok(store.hasEntity(AUTHOR));
  lease.release();
  assert.ok(!store.hasEntity(AUTHOR));
});

test('无 ID 嵌套对象按父级独立存放，不误合并', () => {
  const store = new Store();
  const shared = { city: 'Arrakis', zip: '00001' };
  const tx = store.begin();
  tx.applyResponse({ __typename: 'User', id: 1, home: shared, work: { city: 'Giedi' } });
  tx.applyResponse({ __typename: 'User', id: 2, home: shared });
  tx.commit();
  // structurally equal embedded objects stay independent per parent
  const tx2 = store.begin();
  tx2.writeData('User:1', 'home', { city: 'Caladan', zip: '00001' });
  tx2.commit();
  assert.deepEqual(store.getField('User:1', 'home'), { city: 'Caladan', zip: '00001' });
  assert.deepEqual(store.getField('User:2', 'home'), { city: 'Arrakis', zip: '00001' });
  // sibling field of the same parent untouched as well
  assert.deepEqual(store.getField('User:1', 'work'), { city: 'Giedi' });
});

test('嵌套对象重建：缺失键与显式 null 区分', () => {
  const store = new Store();
  const tx = store.begin();
  tx.writeData('User:1', 'profile', { bio: null, nickname: 'ada' });
  tx.commit();
  const profile = store.getField('User:1', 'profile') as Record<string, unknown>;
  assert.ok(Object.prototype.hasOwnProperty.call(profile, 'bio'));
  assert.equal(profile.bio, null);
  // rebuild replaces the whole object; keys absent from the payload stay absent
  const tx2 = store.begin();
  tx2.writeData('User:1', 'profile', { nickname: 'grace' });
  tx2.commit();
  const rebuilt = store.getField('User:1', 'profile') as Record<string, unknown>;
  assert.equal(rebuilt.nickname, 'grace');
  assert.ok(!Object.prototype.hasOwnProperty.call(rebuilt, 'bio'));
});

test('回滚只撤销自身变更', () => {
  const store = seedStore();
  // transaction rollback
  const tx = store.begin();
  tx.writeField(BOOK1, 'title', 'tx-change');
  tx.rollback();
  assert.equal(store.getField(BOOK1, 'title'), 'Dune');
  // layer rollback keeps other layers
  const layer1 = store.optimistic();
  const layer2 = store.optimistic();
  layer1.writeField(BOOK1, 'title', 'from-layer-1');
  layer2.writeField(BOOK1, 'genre', 'sci-fi');
  layer1.rollback();
  assert.equal(store.getField(BOOK1, 'title'), 'Dune');
  assert.equal(store.getField(BOOK1, 'genre'), 'sci-fi');
  layer2.rollback();
  assert.ok(!store.hasField(BOOK1, 'genre'));
});

test('读者看不到事务中间态，快照在提交前后保持不可变', () => {
  const store = seedStore();
  const before = store.snapshot();
  const tx = store.begin();
  tx.writeField(BOOK1, 'title', 'in-flight');
  // mid-transaction: readers still see committed state
  assert.equal(store.getField(BOOK1, 'title'), 'Dune');
  tx.commit();
  assert.equal(store.getField(BOOK1, 'title'), 'in-flight');
  // the old snapshot object was never mutated
  assert.equal(before.entities.get(BOOK1)!.fields.get('title')!.value, 'Dune');
});

test('状态操作可回放：重放操作日志得到相同状态', () => {
  const store = new Store();
  const tx = store.begin();
  tx.applyResponse({
    __typename: 'Book',
    id: 1,
    title: 'Dune',
    author: { __typename: 'Author', id: 1, name: 'Herbert' },
  });
  tx.applyPage(BOOK1, 'chapters', [{ __typename: 'Chapter', id: 1 }], {
    endCursor: 'c1',
    hasNextPage: true,
  });
  tx.commit();

  const lease = store.acquireLease(['Chapter:1']);
  const layer = store.optimistic();
  layer.writeField(BOOK1, 'title', 'optimistic');
  layer.commit();

  const tx2 = store.begin();
  tx2.applyResponse({ __typename: 'Book', id: 1, title: 'stale' }, { baseVersion: 0 });
  tx2.mergeIdentity('Author', 1, 1);
  tx2.deleteEntity('Chapter:1');
  tx2.commit();
  lease.release();

  const rebuilt = Store.replay(store.opLog);
  assert.deepEqual(serializeState(rebuilt.snapshot()), serializeState(store.snapshot()));
  assert.equal(rebuilt.clock, store.clock);
  assert.equal(rebuilt.getField(BOOK1, 'title'), 'optimistic');
});

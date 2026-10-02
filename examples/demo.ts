import { Store, keyOf } from '../src/index.ts';

const show = (label: string, value: unknown) =>
  console.log(`\n== ${label} ==\n` + JSON.stringify(value, null, 2));

const store = new Store();

// 1. Normalize a nested response into entity tables; refs are shared.
const tx1 = store.begin();
tx1.applyResponse({
  __typename: 'Book',
  id: 1,
  title: 'Dune',
  author: { __typename: 'Author', id: 1, name: 'Herbert' },
});
tx1.applyResponse({
  __typename: 'Book',
  id: 2,
  title: 'Dune Messiah',
  author: { __typename: 'Author', id: 1, name: 'Herbert' },
});
tx1.commit();
show('entity tables after normalize', store.entityKeys());
show('Book:1.author is a shared ref', store.getField('Book:1', 'author'));
show('reverse index of Author:1', store.inboundRefs(keyOf('Author', 1)));

// 2. Partial update keeps unreturned fields.
const tx2 = store.begin();
tx2.applyResponse({ __typename: 'Book', id: 1, title: 'Dune (revised)' });
tx2.commit();
show('partial update keeps author ref', {
  title: store.getField('Book:1', 'title'),
  author: store.getField('Book:1', 'author'),
});

// 3. Paginated relation merges pages.
const tx3 = store.begin();
tx3.applyPage('Book:1', 'chapters', [{ __typename: 'Chapter', id: 1 }, { __typename: 'Chapter', id: 2 }], {
  endCursor: 'c2',
  hasNextPage: true,
});
tx3.commit();
const tx4 = store.begin();
tx4.applyPage('Book:1', 'chapters', [{ __typename: 'Chapter', id: 2 }, { __typename: 'Chapter', id: 3 }], {
  endCursor: 'c3',
  hasNextPage: false,
});
tx4.commit();
show('merged pages (dedup, ordered)', store.getField('Book:1', 'chapters'));

// 4. Optimistic layer + stale server response cannot clobber the user edit.
const layer = store.optimistic();
layer.writeField('Book:1', 'title', 'My draft title');
show('reader sees optimistic overlay', store.getField('Book:1', 'title'));
layer.commit();
const issuedAt = 1; // the stale query was issued back at clock 1
const tx5 = store.begin();
tx5.applyResponse({ __typename: 'Book', id: 1, title: 'stale server value' }, { baseVersion: issuedAt });
tx5.commit();
show('stale response rejected, user edit kept', store.getField('Book:1', 'title'));

// 5. Delete with a live query lease: deferred until release.
const lease = store.acquireLease([keyOf('Author', 1)]);
const tx6 = store.begin();
tx6.deleteEntity(keyOf('Author', 1));
tx6.commit();
show('leased entity survives delete', store.hasEntity(keyOf('Author', 1)));
lease.release();
show('after lease release: gone, refs nulled', {
  exists: store.hasEntity(keyOf('Author', 1)),
  book1Author: store.getField('Book:1', 'author'),
});

// 6. Identity merge repoints references atomically.
const tx7 = store.begin();
tx7.applyResponse({ __typename: 'Author', id: 2, name: 'F. Herbert' });
tx7.commit();
const tx8 = store.begin();
tx8.writeField('Book:2', 'author', { $ref: keyOf('Author', 2) });
tx8.commit();
const tx9 = store.begin();
tx9.mergeIdentity('Author', 2, 1);
tx9.commit();
show('Book:2.author repointed to Author:1', store.getField('Book:2', 'author'));

// 7. Replay the op log into a fresh store: identical state.
const rebuilt = Store.replay(store.opLog);
show('replay produces identical state', JSON.stringify(rebuilt.serialize()) === JSON.stringify(store.serialize()));

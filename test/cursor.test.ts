import assert from 'node:assert/strict';
import { test } from './runner.js';
import { EventStore } from '../src/event-store.js';
import { ResumeServer } from '../src/server.js';
import { CursorError, CursorFilterMismatchError, encodeCursor } from '../src/cursor.js';

function makeServer(): { store: EventStore; server: ResumeServer } {
  const store = new EventStore();
  const server = new ResumeServer(store, { secret: 'test-secret' });
  return { store, server };
}

test('cursor cannot be reused across subscription filters', () => {
  const { server } = makeServer();
  const cursor = server.cursorFor(3, 3, 1, { kinds: ['trade'] });
  assert.throws(() => server.connect({ kinds: ['price'] }, () => {}, cursor), CursorFilterMismatchError);
  assert.throws(() => server.connect({}, () => {}, cursor), CursorFilterMismatchError);
  assert.throws(() => server.connect({ kinds: ['trade', 'price'] }, () => {}, cursor), CursorFilterMismatchError);
  const ok = server.connect({ kinds: ['trade'] }, () => {}, cursor);
  ok.close();
});

test('filter matching is order-insensitive for the same condition', () => {
  const { server } = makeServer();
  const cursor = server.cursorFor(2, 2, 1, { kinds: ['trade', 'price'] });
  const ok = server.connect({ kinds: ['price', 'trade'] }, () => {}, cursor);
  ok.close();
});

test('tampered, malformed and wrong-version cursors are rejected', () => {
  const { server } = makeServer();
  assert.throws(
    () => server.connect({}, () => {}, 'garbage'),
    (err: unknown) => err instanceof CursorError && err.reason === 'malformed',
  );
  const cursor = server.cursorFor(3, 3, 1, {});
  const tampered = cursor.slice(0, -2) + (cursor.endsWith('aa') ? 'bb' : 'aa');
  assert.throws(
    () => server.connect({}, () => {}, tampered),
    (err: unknown) => err instanceof CursorError && err.reason === 'bad-signature',
  );
  const foreign = encodeCursor({ v: 1, fh: 'x', seq: 1, off: 1, ep: 1 }, 'other-secret');
  assert.throws(
    () => server.connect({}, () => {}, foreign),
    (err: unknown) => err instanceof CursorError && err.reason === 'bad-signature',
  );
  const wrongVersion = encodeCursor({ v: 2, fh: 'x', seq: 1, off: 1, ep: 1 }, 'test-secret');
  assert.throws(
    () => server.connect({}, () => {}, wrongVersion),
    (err: unknown) => err instanceof CursorError && err.reason === 'unsupported-version',
  );
});

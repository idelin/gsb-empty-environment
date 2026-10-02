import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MembershipTable } from '../src/membership.ts';
import type { MemberMeta } from '../src/types.ts';

function meta(id: string, heartbeat: number, state: MemberMeta['state'] = 'alive'): MemberMeta {
  return { id, address: { host: '127.0.0.1', port: 1000 }, heartbeat, state, lastUpdate: Date.now() };
}

test('merge learns new members and skips self', () => {
  const table = new MembershipTable('self');
  table.upsert(meta('self', 1));
  const learned = table.merge([meta('a', 1), meta('b', 1), meta('self', 5)]);
  assert.equal(learned, 2);
  assert.equal(table.size, 3);
  assert.equal(table.get('self')!.heartbeat, 1);
});

test('last-writer-wins by heartbeat', () => {
  const table = new MembershipTable('self');
  assert.equal(table.upsert(meta('a', 2)), true);
  assert.equal(table.upsert(meta('a', 1)), false);
  assert.equal(table.get('a')!.heartbeat, 2);
  assert.equal(table.upsert(meta('a', 3, 'suspect')), true);
  assert.equal(table.get('a')!.state, 'suspect');
});

test('alive filters non-alive members', () => {
  const table = new MembershipTable('self');
  table.upsert(meta('a', 1));
  table.upsert(meta('b', 1, 'dead'));
  assert.deepEqual(table.alive().map((m) => m.id), ['a']);
});

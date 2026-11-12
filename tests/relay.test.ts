import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ReplaceableEventIndex,
  getDTagValue,
  getReplaceabilityClass,
  getStorageKey,
  shouldReplace,
} from '../src/relay.ts';
import { makeEvent, PUBKEY_A, PUBKEY_B } from './helpers.ts';
import type { Event } from '../src/types.ts';

test('classifies kinds per NIP-01/NIP-16', () => {
  assert.equal(getReplaceabilityClass(0), 'replaceable');
  assert.equal(getReplaceabilityClass(3), 'replaceable');
  assert.equal(getReplaceabilityClass(10000), 'replaceable');
  assert.equal(getReplaceabilityClass(19999), 'replaceable');
  assert.equal(getReplaceabilityClass(9999), 'regular');
  assert.equal(getReplaceabilityClass(1), 'regular');
  assert.equal(getReplaceabilityClass(20000), 'ephemeral');
  assert.equal(getReplaceabilityClass(29999), 'ephemeral');
  assert.equal(getReplaceabilityClass(30000), 'parameterized');
  assert.equal(getReplaceabilityClass(39999), 'parameterized');
  assert.equal(getReplaceabilityClass(40000), 'regular');
});

test('d tag extraction defaults to the empty string', async () => {
  assert.equal(getDTagValue(await makeEvent({ tags: [] })), '');
  assert.equal(getDTagValue(await makeEvent({ tags: [['p', 'x']] })), '');
  assert.equal(getDTagValue(await makeEvent({ tags: [['d']] })), '');
  assert.equal(
    getDTagValue(await makeEvent({ tags: [['p', 'x'], ['d', 'profile']] })),
    'profile',
  );
});

test('storage keys are scoped by pubkey and kind, and parameterized also by d', async () => {
  const meta = await makeEvent({ pubkey: PUBKEY_A, kind: 0, tags: [] });
  assert.equal(getStorageKey(meta), `r:${PUBKEY_A}:0`);

  const addr = await makeEvent({
    pubkey: PUBKEY_A,
    kind: 30000,
    tags: [['d', 'list-1']],
  });
  assert.equal(getStorageKey(addr), `p:${PUBKEY_A}:30000:list-1`);

  const addrEmptyD = await makeEvent({ pubkey: PUBKEY_A, kind: 30000, tags: [] });
  assert.equal(getStorageKey(addrEmptyD), `p:${PUBKEY_A}:30000:`);

  assert.equal(getStorageKey(await makeEvent({ kind: 1 })), null);
  assert.equal(getStorageKey(await makeEvent({ kind: 20001 })), null);

  const otherAuthor = await makeEvent({ pubkey: PUBKEY_B, kind: 0 });
  assert.notEqual(getStorageKey(meta), getStorageKey(otherAuthor));
});

test('shouldReplace: newer created_at wins, ties broken by smaller id', async () => {
  const older = { id: 'a'.repeat(64), created_at: 100 };
  const newer = { id: 'a'.repeat(64), created_at: 200 };
  assert.equal(shouldReplace(older, newer), true);
  assert.equal(shouldReplace(newer, older), false);

  const tieSmallId = { id: '0'.repeat(64), created_at: 100 };
  const tieLargeId = { id: 'f'.repeat(64), created_at: 100 };
  assert.equal(shouldReplace(tieLargeId, tieSmallId), true);
  assert.equal(shouldReplace(tieSmallId, tieLargeId), false);
});

test('replaying an older replaceable event cannot overwrite newer state', async () => {
  const index = new ReplaceableEventIndex();
  const first = await makeEvent({ kind: 0, created_at: 100 });
  const second = await makeEvent({ kind: 0, created_at: 200 });
  const replay = await makeEvent({ kind: 0, created_at: 150 });

  const d1 = index.ingest(first);
  assert.equal(d1.action, 'store');
  const d2 = index.ingest(second);
  assert.equal(d2.action, 'replace');
  assert.equal(d2.incumbent?.id, first.id);

  const d3 = index.ingest(replay);
  assert.equal(d3.action, 'reject_stale');
  assert.equal(d3.incumbent?.id, second.id);
  assert.equal(index.get(getStorageKey(second) as string)?.id, second.id);

  // arrival order changes nothing: old first then new still ends on new
  const reordered = new ReplaceableEventIndex();
  reordered.ingest(second);
  reordered.ingest(replay);
  reordered.ingest(first);
  assert.equal(reordered.get(getStorageKey(second) as string)?.id, second.id);
});

test('equal timestamps: lexicographically smaller id wins regardless of arrival order', async () => {
  const smallId = await makeEvent({ kind: 3, created_at: 500 });
  const largeId = await makeEvent({ kind: 3, created_at: 500 });
  Object.defineProperty(smallId, 'id', { value: '0'.repeat(64), configurable: true });
  Object.defineProperty(largeId, 'id', { value: 'f'.repeat(64), configurable: true });

  const indexA = new ReplaceableEventIndex();
  indexA.ingest(largeId);
  const decisionA = indexA.ingest(smallId);
  assert.equal(decisionA.action, 'replace');
  assert.equal(indexA.get(getStorageKey(smallId) as string)?.id, '0'.repeat(64));

  const indexB = new ReplaceableEventIndex();
  indexB.ingest(smallId);
  const decisionB = indexB.ingest(largeId);
  assert.equal(decisionB.action, 'reject_stale');
  assert.equal(indexB.get(getStorageKey(smallId) as string)?.id, '0'.repeat(64));
});

test('the identical event arriving again is a duplicate, not a replacement', async () => {
  const index = new ReplaceableEventIndex();
  const event = await makeEvent({ kind: 10000, created_at: 10 });
  index.ingest(event);
  const again = index.ingest({ ...event });
  assert.equal(again.action, 'duplicate');
  assert.equal(index.size, 1);
});

test('parameterized events are keyed independently per d tag', async () => {
  const index = new ReplaceableEventIndex();
  const list1v1 = await makeEvent({
    kind: 30000,
    created_at: 10,
    tags: [['d', 'list-1']],
  });
  const list1v2 = await makeEvent({
    kind: 30000,
    created_at: 20,
    tags: [['d', 'list-1']],
  });
  const list2 = await makeEvent({
    kind: 30000,
    created_at: 5,
    tags: [['d', 'list-2']],
  });

  assert.equal(index.ingest(list1v1).action, 'store');
  assert.equal(index.ingest(list1v2).action, 'replace');
  assert.equal(index.ingest(list2).action, 'store');
  assert.equal(index.size, 2);

  const oldList1 = await makeEvent({
    kind: 30000,
    created_at: 1,
    tags: [['d', 'list-1']],
  });
  assert.equal(index.ingest(oldList1).action, 'reject_stale');
  assert.equal(index.get(getStorageKey(list1v2) as string)?.id, list1v2.id);
});

test('parameterized events with no d tag share the empty-string key', async () => {
  const index = new ReplaceableEventIndex();
  const v1 = await makeEvent({ kind: 30001, created_at: 1, tags: [] });
  const v2 = await makeEvent({ kind: 30001, created_at: 2, tags: [['p', 'x']] });
  index.ingest(v1);
  const decision = index.ingest(v2);
  assert.equal(decision.action, 'replace');
  assert.equal(decision.key, `p:${PUBKEY_A}:30001:`);
});

test('regular events are always stored and ephemeral events are never indexed', async () => {
  const index = new ReplaceableEventIndex();
  const regular1 = await makeEvent({ kind: 1, created_at: 100 });
  const regular2 = await makeEvent({ kind: 1, created_at: 90 });
  assert.equal(index.ingest(regular1).action, 'store');
  assert.equal(index.ingest(regular2).action, 'store');
  assert.equal(index.size, 0);

  const ephemeral = await makeEvent({ kind: 20001 });
  const decision = index.ingest(ephemeral);
  assert.equal(decision.action, 'ephemeral');
  assert.equal(index.size, 0);
});

test('different pubkeys never collide in the index', async () => {
  const index = new ReplaceableEventIndex();
  const a = await makeEvent({ pubkey: PUBKEY_A, kind: 0, created_at: 1 });
  const b = await makeEvent({ pubkey: PUBKEY_B, kind: 0, created_at: 2 });
  index.ingest(a);
  index.ingest(b);
  assert.equal(index.size, 2);
  assert.equal(index.get(getStorageKey(a) as string)?.pubkey, PUBKEY_A);
  assert.equal(index.get(getStorageKey(b) as string)?.pubkey, PUBKEY_B);
});

test('end-to-end: validate then ingest rejects a stale replay at the door', async () => {
  const { validateEvent } = await import('../src/validate.ts');
  const { mockVerifier } = await import('./helpers.ts');
  const index = new ReplaceableEventIndex();
  const now = 1_700_000_000;
  const options = { verifySchnorr: mockVerifier(), now: now + 1000 };

  const events: Event[] = [];
  for (const created_at of [100, 200, 150]) {
    const event = await makeEvent({ kind: 10001, created_at });
    const result = await validateEvent(event, options);
    assert.equal(result.ok, true);
    if (result.ok) {
      events.push(result.event);
    }
  }

  assert.equal(index.ingest(events[0]).action, 'store');
  assert.equal(index.ingest(events[1]).action, 'replace');
  assert.equal(index.ingest(events[2]).action, 'reject_stale');
  assert.equal(index.get(getStorageKey(events[1]) as string)?.created_at, 200);
});

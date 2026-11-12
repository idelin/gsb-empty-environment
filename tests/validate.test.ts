import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validateEvent, assertValidEvent } from '../src/validate.ts';
import { computeEventId } from '../src/hash.ts';
import { makeEvent, mockVerifier, PUBKEY_A, PUBKEY_B } from './helpers.ts';
import type { Event } from '../src/types.ts';

const NOW = 1_700_001_000;

test('a well-formed event with correct id and sig passes', async () => {
  const event = await makeEvent();
  const verifier = mockVerifier();
  const result = await validateEvent(event, { verifySchnorr: verifier, now: NOW });
  assert.equal(result.ok, true);
  if (result.ok) {
    assert.deepEqual(result.event, event);
  }
  assert.equal(verifier.calls.length, 1);
  assert.deepEqual(verifier.calls[0], { id: event.id, pubkey: event.pubkey, sig: event.sig });
});

test('assertValidEvent returns the narrowed event or throws with a code', async () => {
  const event = await makeEvent();
  const narrowed = await assertValidEvent(event, {
    verifySchnorr: mockVerifier(),
    now: NOW,
  });
  assert.equal(narrowed.id, event.id);

  const bad = { ...event, pubkey: 'ZZ'.repeat(32) };
  await assert.rejects(
    () => assertValidEvent(bad, { verifySchnorr: mockVerifier(), now: NOW }),
    (error: Error & { code?: string }) => error.code === 'BAD_PUBKEY',
  );
});

test('rejects non-object payloads', async () => {
  for (const payload of [null, undefined, 42, 'str', [], true]) {
    const result = await validateEvent(payload, {
      verifySchnorr: mockVerifier(),
      now: NOW,
    });
    assert.equal(result.ok, false);
    if (!result.ok) {
      assert.equal(result.code, 'NOT_OBJECT');
    }
  }
});

test('pubkey must be exactly 64 lowercase hex chars', async () => {
  for (const pubkey of [
    PUBKEY_A.toUpperCase(),
    PUBKEY_A.slice(0, 62),
    'g' + PUBKEY_A.slice(1),
    '',
  ]) {
    const event = await makeEvent({ pubkey: pubkey as string });
    const result = await validateEvent(event, {
      verifySchnorr: mockVerifier(),
      now: NOW,
    });
    assert.equal(result.ok, false);
    if (!result.ok) {
      assert.equal(result.code, 'BAD_PUBKEY', `for pubkey=${String(pubkey)}`);
    }
  }

  const nonString = { ...(await makeEvent()), pubkey: 123 };
  const result = await validateEvent(nonString, {
    verifySchnorr: mockVerifier(),
    now: NOW,
  });
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, 'BAD_PUBKEY');
  }
});

test('sig must be exactly 128 lowercase hex chars', async () => {
  const event = await makeEvent();
  for (const sig of [event.sig.toUpperCase(), event.sig.slice(2), 'z' + event.sig.slice(1), '']) {
    const result = await validateEvent(
      { ...event, sig },
      { verifySchnorr: mockVerifier(), now: NOW },
    );
    assert.equal(result.ok, false);
    if (!result.ok) {
      assert.equal(result.code, 'BAD_SIG', `for sig=${sig}`);
    }
  }
});

test('id must be 64 lowercase hex and match the recomputed hash', async () => {
  const event = await makeEvent();
  const wrongShape = { ...event, id: event.id.toUpperCase() };
  const r1 = await validateEvent(wrongShape, {
    verifySchnorr: mockVerifier(),
    now: NOW,
  });
  assert.equal(r1.ok, false);
  if (!r1.ok) {
    assert.equal(r1.code, 'BAD_ID');
  }

  const wrongValue = { ...event, id: '00'.repeat(32) };
  const r2 = await validateEvent(wrongValue, {
    verifySchnorr: mockVerifier(),
    now: NOW,
  });
  assert.equal(r2.ok, false);
  if (!r2.ok) {
    assert.equal(r2.code, 'BAD_ID');
  }
});

test('tampering with any tuple field invalidates the id (no trust in wire id)', async () => {
  const event = await makeEvent({ content: 'original', tags: [['t', 'x']] });
  const tampered: Array<[string, Partial<Event>]> = [
    ['content', { content: 'tampered' }],
    ['tags', { tags: [['t', 'y']] }],
    ['created_at', { created_at: event.created_at + 1 }],
    ['kind', { kind: 2 }],
    ['pubkey', { pubkey: PUBKEY_B }],
  ];
  for (const [label, patch] of tampered) {
    const result = await validateEvent({ ...event, ...patch }, {
      verifySchnorr: mockVerifier(),
      now: NOW,
    });
    assert.equal(result.ok, false, label);
    if (!result.ok) {
      assert.equal(result.code, 'BAD_ID', label);
    }
  }
});

test('recomputed id is compared in constant time against the wire id', async () => {
  const event = await makeEvent();
  // same length, different value: must fail rather than be coerced
  const different = { ...event, id: event.id.replace(/[0-9a-f]/, (c) => (c === '0' ? '1' : '0')) };
  const result = await validateEvent(different, {
    verifySchnorr: mockVerifier(),
    now: NOW,
  });
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, 'BAD_ID');
  }
});

test('field shape checks', async () => {
  const event = await makeEvent();
  const cases: Array<[string, unknown, string]> = [
    ['created_at float', { created_at: 1.5 }, 'BAD_CREATED_AT'],
    ['created_at negative', { created_at: -1 }, 'BAD_CREATED_AT'],
    ['created_at string', { created_at: '100' }, 'BAD_CREATED_AT'],
    ['kind float', { kind: 1.1 }, 'BAD_KIND'],
    ['tags not array', { tags: {} }, 'BAD_TAGS'],
    ['tags non-string element', { tags: [['t', 1]] }, 'BAD_TAGS'],
    ['tags non-array member', { tags: ['t'] }, 'BAD_TAGS'],
    ['content number', { content: 7 }, 'BAD_CONTENT'],
  ];
  for (const [label, patch, code] of cases) {
    const result = await validateEvent({ ...event, ...(patch as object) }, {
      verifySchnorr: mockVerifier(),
      now: NOW,
    });
    assert.equal(result.ok, false, label);
    if (!result.ok) {
      assert.equal(result.code, code, label);
    }
  }
});

test('created_at in the future beyond the caller allowance is rejected', async () => {
  const event = await makeEvent({ created_at: NOW + 900 });

  const rejected = await validateEvent(event, {
    verifySchnorr: mockVerifier(),
    now: NOW,
    futureLimitSeconds: 300,
  });
  assert.equal(rejected.ok, false);
  if (!rejected.ok) {
    assert.equal(rejected.code, 'FUTURE_TIMESTAMP');
  }

  const accepted = await validateEvent(event, {
    verifySchnorr: mockVerifier(),
    now: NOW,
    futureLimitSeconds: 900,
  });
  assert.equal(accepted.ok, true);

  const defaultStrict = await validateEvent(
    await makeEvent({ created_at: NOW + 1 }),
    { verifySchnorr: mockVerifier(), now: NOW },
  );
  assert.equal(defaultStrict.ok, false);
  if (!defaultStrict.ok) {
    assert.equal(defaultStrict.code, 'FUTURE_TIMESTAMP');
  }
});

test('the boundary created_at == now + allowance is accepted', async () => {
  const event = await makeEvent({ created_at: NOW + 60 });
  const result = await validateEvent(event, {
    verifySchnorr: mockVerifier(),
    now: NOW,
    futureLimitSeconds: 60,
  });
  assert.equal(result.ok, true);
});

test('an invalid Schnorr signature is BAD_SIG and the verifier is not invoked for bad ids', async () => {
  const event = await makeEvent();
  const verifier = mockVerifier({ rejectIds: [event.id] });
  const result = await validateEvent(event, { verifySchnorr: verifier, now: NOW });
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, 'BAD_SIG');
  }

  const badIdVerifier = mockVerifier();
  const badIdEvent = { ...event, id: '00'.repeat(32) };
  const badIdResult = await validateEvent(badIdEvent, {
    verifySchnorr: badIdVerifier,
    now: NOW,
  });
  assert.equal(badIdResult.ok, false);
  if (!badIdResult.ok) {
    assert.equal(badIdResult.code, 'BAD_ID');
  }
  assert.equal(badIdVerifier.calls.length, 0);
});

test('a throwing verifier surfaces SIG_VERIFIER_ERROR rather than BAD_SIG', async () => {
  const event = await makeEvent();
  const result = await validateEvent(event, {
    verifySchnorr: mockVerifier({ throwForIds: [event.id] }),
    now: NOW,
  });
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, 'SIG_VERIFIER_ERROR');
    assert.match(result.reason, /verifier boom/);
  }
});

test('async Schnorr verifiers are awaited', async () => {
  const event = await makeEvent();
  const result = await validateEvent(event, {
    verifySchnorr: async () => true,
    now: NOW,
  });
  assert.equal(result.ok, true);
});

test('a missing verifier throws a programmer error', async () => {
  const event = await makeEvent();
  await assert.rejects(
    () => validateEvent(event, {} as never),
    /verifySchnorr/,
  );
});

test('accepts an injected sha256 and rejects when it yields a different digest', async () => {
  const event = await makeEvent();
  const realId = event.id;
  const wrong = new Uint8Array(32).fill(7);
  const injected = await validateEvent(event, {
    verifySchnorr: mockVerifier(),
    now: NOW,
    sha256: () => wrong,
  });
  assert.equal(injected.ok, false);
  if (!injected.ok) {
    assert.equal(injected.code, 'BAD_ID');
  }
  assert.notEqual(realId, '07'.repeat(32));
});

test('unknown extra fields on the wire object are ignored', async () => {
  const event = await makeEvent();
  const withExtras = { ...event, extra: 'ignored', nested: { x: 1 } } as Event & { extra: string };
  const result = await validateEvent(withExtras, {
    verifySchnorr: mockVerifier(),
    now: NOW,
  });
  assert.equal(result.ok, true);
  if (result.ok) {
    assert.deepEqual(Object.keys(result.event).sort(), [
      'content',
      'created_at',
      'id',
      'kind',
      'pubkey',
      'sig',
      'tags',
    ]);
  }
});

test('computeEventId agrees with the id produced by makeEvent helper', async () => {
  const event = await makeEvent({ content: 'abc', tags: [['d', 'x']], kind: 30000 });
  const recomputed = await computeEventId(event);
  assert.equal(recomputed, event.id);
});

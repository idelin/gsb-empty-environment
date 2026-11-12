import { test } from 'node:test';
import assert from 'node:assert/strict';
import { serializeCanonicalEvent } from '../src/canonical.ts';
import { computeEventId } from '../src/hash.ts';
import { validateEvent } from '../src/validate.ts';
import { mockVerifier, PUBKEY_A } from './helpers.ts';
import type { CanonicalEventTuple } from '../src/types.ts';

function tuple(overrides: Partial<CanonicalEventTuple> = {}): CanonicalEventTuple {
  return [
    0,
    overrides[1] ?? PUBKEY_A,
    overrides[2] ?? 1_700_000_000,
    overrides[3] ?? 1,
    overrides[4] ?? [],
    overrides[5] ?? '',
  ];
}

test('serializes the fixed NIP-01 tuple with no whitespace', () => {
  const json = serializeCanonicalEvent(
    tuple({
      4: [['e', 'deadbeef'], ['p', 'aa']],
      5: 'hello',
    }),
  );
  assert.equal(
    json,
    `[0,"${PUBKEY_A}",1700000000,1,[["e","deadbeef"],["p","aa"]],"hello"]`,
  );
  assert.doesNotMatch(json, /\s/);
});

test('escapes exactly the seven mandated characters and nothing else', () => {
  const content = 'a\n"b\\c\rd\te\bf\f/g\u0000h\u0001i';
  const json = serializeCanonicalEvent(tuple({ 5: content }));
  assert.equal(
    json,
    `[0,"${PUBKEY_A}",1700000000,1,[],"a\\n\\"b\\\\c\\rd\\te\\bf\\f/g\u0000h\u0001i"]`,
  );
  // solidus and control chars other than the seven stay verbatim
  assert.ok(json.includes('/g'));
  assert.ok(!json.includes('\\/'));
  assert.ok(!json.includes('\\u'));
});

test('matches JSON.stringify on canonical inputs (parity sanity check)', () => {
  const t = tuple({
    4: [['emoji', '😀'], ['quote', 'he said "hi"']],
    5: 'line1\nline2\ttab',
  });
  assert.equal(
    serializeCanonicalEvent(t),
    JSON.stringify([t[0], t[1], t[2], t[3], t[4], t[5]]),
  );
});

test('empty tags/content and NUL bytes are serialized deterministically', () => {
  const empty = serializeCanonicalEvent(tuple());
  assert.equal(empty, `[0,"${PUBKEY_A}",1700000000,1,[],""]`);

  const withNul = serializeCanonicalEvent(tuple({ 5: 'a\u0000b' }));
  assert.equal(withNul, `[0,"${PUBKEY_A}",1700000000,1,[],"a\u0000b"]`);
  assert.ok(!withNul.includes('\\u0000'));

  const emptyTag = serializeCanonicalEvent(tuple({ 4: [[]] }));
  assert.equal(emptyTag, `[0,"${PUBKEY_A}",1700000000,1,[[]],""]`);
});

test('parity with JSON.stringify where the two escape rules agree', () => {
  const tricky = [
    'café',
    '😀\uD83D\uDE00',
    '/\\//\n\r\t\b\f"',
    'printable ~`!@#$%^&*()_+-=[]{}|;:,.<>?',
  ];
  for (const content of tricky) {
    const t = tuple({ 5: content, 4: [['x', content], []] });
    assert.equal(
      serializeCanonicalEvent(t),
      JSON.stringify([t[0], t[1], t[2], t[3], t[4], t[5]]),
      `parity failed for ${JSON.stringify(content)}`,
    );
  }
});

test('other control chars stay verbatim whereas JSON.stringify would \\u-escape them', () => {
  const content = '\u0000\u0001\u001f\u007f';
  const json = serializeCanonicalEvent(tuple({ 5: content }));
  assert.ok(json.includes(`"${content}"`));
  assert.ok(!json.includes('\\u'));
  const t = tuple({ 5: content });
  assert.notEqual(
    json,
    JSON.stringify([t[0], t[1], t[2], t[3], t[4], t[5]]),
  );
});

test('lone surrogates are emitted verbatim, not \\u-escaped (restricted escape set)', () => {
  // JSON.stringify would emit "\\ud800"; the NIP-01 restricted escape set has
  // no \u escape, so the canonical serializer keeps the raw code unit. This
  // keeps the rule "only seven characters are escaped" absolute.
  const json = serializeCanonicalEvent(tuple({ 5: 'x\uD800y' }));
  assert.equal(json, `[0,"${PUBKEY_A}",1700000000,1,[],"x\uD800y"]`);
  assert.ok(!json.includes('\\ud800'));
  const t = tuple({ 5: 'x\uD800y' });
  assert.notEqual(
    json,
    JSON.stringify([t[0], t[1], t[2], t[3], t[4], t[5]]),
  );
});

test('two events with identical logical content always produce the same id', async () => {
  const base = {
    pubkey: PUBKEY_A,
    created_at: 1_700_000_000,
    kind: 1,
    tags: [['t', 'nostr']],
    content: 'new\nline "quote" \\ slash / solidus',
  };
  const id1 = await computeEventId(base);
  const id2 = await computeEventId({ ...base, tags: [...base.tags] });
  assert.equal(id1, id2);
  assert.match(id1, /^[0-9a-f]{64}$/);
});

test('an id forged with a non-canonical \\u000a escape is rejected', async () => {
  // The attacker computes the id over a JSON text that escapes the newline
  // as \u000a. The actual string content still contains a real newline.
  const content = 'two\nlines';
  const fields = {
    pubkey: PUBKEY_A,
    created_at: 1_700_000_000,
    kind: 1,
    tags: [] as string[][],
    content,
  };
  const forgedSerialization = JSON.stringify([
    0,
    fields.pubkey,
    fields.created_at,
    fields.kind,
    fields.tags,
    content.replace('\n', '\\u000a'),
  ]);
  assert.ok(forgedSerialization.includes('\\u000a'));
  const forgedId = await computeEventIdFromText(forgedSerialization);

  const result = await validateEvent(
    { ...fields, id: forgedId, sig: 'cc'.repeat(64) },
    { verifySchnorr: mockVerifier(), now: 1_700_000_100 },
  );
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, 'BAD_ID');
  }
});

test('an id forged with a solidus escape \\/ is rejected', async () => {
  const fields = {
    pubkey: PUBKEY_A,
    created_at: 1_700_000_000,
    kind: 1,
    tags: [] as string[][],
    content: 'a/b',
  };
  const forgedSerialization = JSON.stringify([
    0,
    fields.pubkey,
    fields.created_at,
    fields.kind,
    fields.tags,
    'a\\/b',
  ]);
  const forgedId = await computeEventIdFromText(forgedSerialization);

  const result = await validateEvent(
    { ...fields, id: forgedId, sig: 'cc'.repeat(64) },
    { verifySchnorr: mockVerifier(), now: 1_700_000_100 },
  );
 assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, 'BAD_ID');
  }
});

async function computeEventIdFromText(text: string): Promise<string> {
  const { createHash } = await import('node:crypto');
  return createHash('sha256').update(new TextEncoder().encode(text)).digest('hex');
}

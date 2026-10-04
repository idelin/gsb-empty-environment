import * as assert from 'assert';
import {
  negotiate,
  verifyTranscript,
  transcriptBinds,
  computeRoundId,
  hashDeclaration,
  PartyDeclaration,
} from '../src/index';

let passed = 0;
let failed = 0;

function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log(`ok   ${name}`);
  } catch (err) {
    failed++;
    console.error(`FAIL ${name}`);
    console.error(err);
  }
}

function decl(
  party: string,
  round: string,
  caps: PartyDeclaration['capabilities'],
): PartyDeclaration {
  return { party, round, capabilities: caps };
}

// 1. Basic: intersection only, highest combined priority wins.
test('selects highest-priority common configuration', () => {
  const a = decl('alice', 'r1', [
    { name: 'h264', priority: 5 },
    { name: 'h265', priority: 9 },
    { name: 'av1', priority: 3 },
  ]);
  const b = decl('bob', 'r1', [
    { name: 'h264', priority: 5 },
    { name: 'h265', priority: 1 },
  ]);
  const res = negotiate(a, b);
  assert.strictEqual(res.status, 'ok');
  if (res.status !== 'ok') return;
  // Both common caps are compatible; selecting both maximizes weight.
  assert.deepStrictEqual(res.config, ['h264', 'h265']);
  assert.strictEqual(res.score, 10 + 10);
});

// 2. Mutex excludes the weaker option.
test('respects mutual exclusion', () => {
  const a = decl('alice', 'r1', [
    { name: 'tcp', priority: 4, conflicts: ['udp'] },
    { name: 'udp', priority: 9 },
  ]);
  const b = decl('bob', 'r1', [
    { name: 'tcp', priority: 4 },
    { name: 'udp', priority: 9 },
  ]);
  const res = negotiate(a, b);
  assert.strictEqual(res.status, 'ok');
  if (res.status !== 'ok') return;
  assert.deepStrictEqual(res.config, ['udp']);
  assert.strictEqual(res.score, 18);
});

// 3. Dependency pulls in a required capability.
test('dependency closure is included', () => {
  const a = decl('alice', 'r1', [
    { name: 'tls13', priority: 8, requires: ['sni'] },
    { name: 'sni', priority: 1 },
  ]);
  const b = decl('bob', 'r1', [
    { name: 'tls13', priority: 8 },
    { name: 'sni', priority: 1 },
  ]);
  const res = negotiate(a, b);
  assert.strictEqual(res.status, 'ok');
  if (res.status !== 'ok') return;
  assert.deepStrictEqual(res.config, ['sni', 'tls13']);
});

// 3b. Dependency whose cost outweighs its benefit is skipped.
test('solver weighs dependency cost', () => {
  const a = decl('alice', 'r1', [
    { name: 'fancy', priority: 5, requires: ['heavy'] },
    { name: 'heavy', priority: -100 },
    { name: 'plain', priority: 4 },
  ]);
  const b = decl('bob', 'r1', [
    { name: 'fancy', priority: 5 },
    { name: 'heavy', priority: 1 },
    { name: 'plain', priority: 4 },
  ]);
  const res = negotiate(a, b);
  assert.strictEqual(res.status, 'ok');
  if (res.status !== 'ok') return;
  assert.deepStrictEqual(res.config, ['plain']);
});

// 4. Mandatory capability not offered by the peer -> minimal core.
test('conflict: mandatory capability not commonly supported', () => {
  const a = decl('alice', 'r1', [
    { name: 'webrtc', priority: 5, mandatory: true },
    { name: 'h264', priority: 1 },
  ]);
  const b = decl('bob', 'r1', [{ name: 'h264', priority: 1 }]);
  const res = negotiate(a, b);
  assert.strictEqual(res.status, 'conflict');
  if (res.status !== 'conflict') return;
  assert.deepStrictEqual(
    res.conflict.constraints.map((c) => c.kind + ':' + c.capability),
    ['mandatory:webrtc'],
  );
  assert.deepStrictEqual(res.conflict.unsupported, ['webrtc']);
  assert.ok(res.conflict.explanation.indexOf('webrtc') >= 0);
});

// 5. Mandatory chain requiring an unsupported capability.
test('conflict: mandatory requires unsupported capability', () => {
  const a = decl('alice', 'r1', [
    { name: 'quic', priority: 5, mandatory: true, requires: ['udp'] },
    { name: 'udp', priority: 1 },
  ]);
  const b = decl('bob', 'r1', [{ name: 'quic', priority: 5 }]);
  const res = negotiate(a, b);
  assert.strictEqual(res.status, 'conflict');
  if (res.status !== 'conflict') return;
  const keys = res.conflict.constraints
    .map((c) => `${c.kind}:${c.capability}:${c.other || ''}`)
    .sort();
  assert.deepStrictEqual(keys, ['mandatory:quic:', 'requires:quic:udp']);
  assert.deepStrictEqual(res.conflict.unsupported, ['udp']);
});

// 6. Two mandatory capabilities that are mutually exclusive.
test('conflict: mandatory pair under mutex', () => {
  const a = decl('alice', 'r1', [
    { name: 'x', priority: 5, mandatory: true, conflicts: ['y'] },
    { name: 'y', priority: 1 },
  ]);
  const b = decl('bob', 'r1', [
    { name: 'x', priority: 5 },
    { name: 'y', priority: 5, mandatory: true },
  ]);
  const res = negotiate(a, b);
  assert.strictEqual(res.status, 'conflict');
  if (res.status !== 'conflict') return;
  const keys = res.conflict.constraints
    .map((c) => `${c.kind}:${c.capability}:${c.other || ''}`)
    .sort();
  assert.deepStrictEqual(keys, [
    'conflicts:x:y',
    'mandatory:x:',
    'mandatory:y:',
  ]);
});

// 7. Core minimality: unrelated constraints stay out of the core.
test('conflict core is minimal', () => {
  const a = decl('alice', 'r1', [
    { name: 'x', priority: 5, mandatory: true, conflicts: ['y'] },
    { name: 'y', priority: 1 },
    { name: 'opt1', priority: 1, requires: ['opt2'] },
    { name: 'opt2', priority: 1 },
  ]);
  const b = decl('bob', 'r1', [
    { name: 'x', priority: 5 },
    { name: 'y', priority: 5, mandatory: true },
    { name: 'opt1', priority: 1 },
    { name: 'opt2', priority: 1 },
  ]);
  const res = negotiate(a, b);
  assert.strictEqual(res.status, 'conflict');
  if (res.status !== 'conflict') return;
  assert.strictEqual(res.conflict.constraints.length, 3);
  for (const c of res.conflict.constraints) {
    assert.ok(['x', 'y'].indexOf(c.capability) >= 0);
  }
});

// 8. Transcript binds both declarations; verification passes.
test('transcript verifies against original declarations', () => {
  const a = decl('alice', 'round-1', [{ name: 'h264', priority: 5 }]);
  const b = decl('bob', 'round-1', [{ name: 'h264', priority: 5 }]);
  const res = negotiate(a, b);
  assert.strictEqual(res.status, 'ok');
  const v = verifyTranscript(res.transcript, [a, b]);
  assert.strictEqual(v.valid, true, v.errors.join('; '));
  assert.deepStrictEqual(v.replayed, []);
  assert.ok(transcriptBinds(res.transcript, a));
  assert.ok(transcriptBinds(res.transcript, b));
});

// 9. Replaying a stale declaration is recognized as another round.
test('replayed declaration is detected as another round', () => {
  const aRound1 = decl('alice', 'round-1', [{ name: 'h264', priority: 5 }]);
  const aRound2 = decl('alice', 'round-2', [{ name: 'h264', priority: 5 }]);
  const b = decl('bob', 'round-2', [{ name: 'h264', priority: 5 }]);

  const fresh = negotiate(aRound2, b);
  assert.strictEqual(fresh.status, 'ok');

  // Attacker replays alice's round-1 declaration against the round-2 transcript.
  const v = verifyTranscript(fresh.transcript, [aRound1, b]);
  assert.strictEqual(v.valid, false);
  assert.deepStrictEqual(v.replayed, ['alice']);
  assert.strictEqual(transcriptBinds(fresh.transcript, aRound1), false);

  // The two rounds produce distinct round ids.
  const old = negotiate(aRound1, decl('bob', 'round-1', b.capabilities));
  assert.notStrictEqual(
    computeRoundId(aRound1, decl('bob', 'round-1', b.capabilities)),
    computeRoundId(aRound2, b),
  );
  assert.notStrictEqual(old.transcript.roundId, fresh.transcript.roundId);
});

// 10. Tampering with the transcript is detected.
test('tampered transcript fails verification', () => {
  const a = decl('alice', 'r1', [{ name: 'h264', priority: 5 }]);
  const b = decl('bob', 'r1', [{ name: 'h264', priority: 5 }]);
  const res = negotiate(a, b);
  assert.strictEqual(res.status, 'ok');
  if (res.status !== 'ok') return;
  const tampered = { ...res.transcript, config: ['h264', 'av1'] };
  const v = verifyTranscript(tampered, [a, b]);
  assert.strictEqual(v.valid, false);
  assert.ok(v.errors.length > 0);
});

// 11. Conflict transcripts verify too.
test('conflict transcript verifies', () => {
  const a = decl('alice', 'r1', [
    { name: 'only-a', priority: 5, mandatory: true },
  ]);
  const b = decl('bob', 'r1', [{ name: 'other', priority: 5 }]);
  const res = negotiate(a, b);
  assert.strictEqual(res.status, 'conflict');
  const v = verifyTranscript(res.transcript, [a, b]);
  assert.strictEqual(v.valid, true, v.errors.join('; '));
});

// 12. Declaration hashing is order-insensitive; negotiation is deterministic.
test('canonical hashing and deterministic outcome', () => {
  const a1 = decl('alice', 'r1', [
    { name: 'b', priority: 2, requires: ['c'] },
    { name: 'a', priority: 1 },
    { name: 'c', priority: 1 },
  ]);
  const a2: PartyDeclaration = {
    party: 'alice',
    round: 'r1',
    capabilities: [
      { name: 'c', priority: 1 },
      { name: 'a', priority: 1 },
      { name: 'b', priority: 2, requires: ['c'] },
    ],
  };
  assert.strictEqual(hashDeclaration(a1), hashDeclaration(a2));
  const b = decl('bob', 'r1', [
    { name: 'a', priority: 1 },
    { name: 'b', priority: 2 },
    { name: 'c', priority: 1 },
  ]);
  const r1 = negotiate(a1, b);
  const r2 = negotiate(a2, b);
  assert.strictEqual(r1.status, 'ok');
  assert.strictEqual(r2.status, 'ok');
  if (r1.status !== 'ok' || r2.status !== 'ok') return;
  assert.deepStrictEqual(r1.config, r2.config);
  assert.strictEqual(r1.transcript.digest, r2.transcript.digest);
});

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);

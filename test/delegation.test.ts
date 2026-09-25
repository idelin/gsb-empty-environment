import test from 'node:test';
import assert from 'node:assert/strict';
import { KeyRegistry, canonicalize } from '../src/crypto.ts';
import { DelegationLedger, DelegationError } from '../src/ledger.ts';
import type { Condition, Delegation } from '../src/types.ts';

const T0 = 1_000;
const HOUR = 3_600;

interface Fixture {
  ledger: DelegationLedger;
  rootToAlice: Delegation;
  aliceToBob: Delegation;
  bobToCarol: Delegation;
}

const analystPolicy: Condition = {
  op: 'and',
  conditions: [
    { op: 'in', attr: 'dept', set: ['eng', 'ops'] },
    { op: 'gte', attr: 'clearance', value: 2 },
  ],
};

/** root -> alice -> bob -> carol, attenuating at every hop. */
function makeFixture(): Fixture {
  const keys = new KeyRegistry();
  for (const principal of ['root', 'alice', 'bob', 'carol']) {
    keys.generate(principal);
  }
  const ledger = new DelegationLedger(keys);

  const rootToAlice = ledger.issueRoot({
    subject: 'alice',
    resources: ['db/users', 'db/orders', 'db/audit'],
    actions: ['read', 'write'],
    maxDepth: 3,
    delegatable: true,
    notBefore: T0,
    expiresAt: T0 + 10 * HOUR,
  });
  const aliceToBob = ledger.delegate({
    parentId: rootToAlice.id,
    subject: 'bob',
    resources: ['db/users', 'db/orders'],
    actions: ['read', 'write'],
    maxDepth: 2,
    delegatable: true,
    notBefore: T0,
    expiresAt: T0 + 5 * HOUR,
    now: T0,
  });
  const bobToCarol = ledger.delegate({
    parentId: aliceToBob.id,
    subject: 'carol',
    resources: ['db/users'],
    actions: ['read'],
    policy: analystPolicy,
    maxDepth: 0,
    delegatable: false,
    notBefore: T0,
    expiresAt: T0 + 2 * HOUR,
    now: T0,
  });
  return { ledger, rootToAlice, aliceToBob, bobToCarol };
}

test('valid chain verifies and the proof lists every link root-first', () => {
  const { ledger, rootToAlice, aliceToBob, bobToCarol } = makeFixture();
  const proof = ledger.verifyChain(bobToCarol.id, T0 + HOUR);

  assert.equal(proof.valid, true);
  assert.equal(proof.failure, null);
  assert.deepEqual(
    proof.chain.map((link) => link.tokenId),
    [rootToAlice.id, aliceToBob.id, bobToCarol.id],
  );
  for (const link of proof.chain) {
    assert.ok(link.checks.length > 0);
    assert.ok(link.checks.every((check) => check.ok), JSON.stringify(link.checks));
  }
  JSON.parse(JSON.stringify(proof)); // proof must stay JSON-serializable
});

test('authorize allows in-scope access with satisfying attributes', () => {
  const { ledger, bobToCarol } = makeFixture();
  const result = ledger.authorize({
    tokenId: bobToCarol.id,
    resource: 'db/users',
    action: 'read',
    attributes: { dept: 'eng', clearance: 3 },
    now: T0 + HOUR,
  });
  assert.equal(result.allowed, true);
  assert.deepEqual(result.proof.policyTrace, [
    { tokenId: bobToCarol.id, ok: true, attributesRead: ['clearance', 'dept'] },
  ]);
});

test('authorize denies out-of-scope resource/action and failing policy', () => {
  const { ledger, bobToCarol } = makeFixture();
  const base = { tokenId: bobToCarol.id, now: T0 + HOUR };

  assert.equal(
    ledger.authorize({ ...base, resource: 'db/orders', action: 'read' }).allowed,
    false,
  );
  assert.equal(
    ledger.authorize({ ...base, resource: 'db/users', action: 'write' }).allowed,
    false,
  );
  const policyDenied = ledger.authorize({
    ...base,
    resource: 'db/users',
    action: 'read',
    attributes: { dept: 'eng', clearance: 1 },
  });
  assert.equal(policyDenied.allowed, false);
  assert.match(policyDenied.reason, /policy/);
  // Missing explicit attributes cannot satisfy a policy.
  assert.equal(
    ledger.authorize({ ...base, resource: 'db/users', action: 'read' }).allowed,
    false,
  );
});

test('depth budget and non-delegatable marker are enforced', () => {
  const { ledger, aliceToBob, bobToCarol } = makeFixture();

  assert.throws(
    () =>
      ledger.delegate({
        parentId: bobToCarol.id,
        subject: 'dave',
        resources: ['db/users'],
        actions: ['read'],
        maxDepth: 0,
        delegatable: false,
        notBefore: T0,
        expiresAt: T0 + HOUR,
        now: T0,
      }),
    /non-delegatable/,
  );

  assert.throws(
    () =>
      ledger.delegate({
        parentId: aliceToBob.id,
        subject: 'dave',
        resources: ['db/users'],
        actions: ['read'],
        maxDepth: 2, // parent budget is 2, child may take at most 1
        delegatable: true,
        notBefore: T0,
        expiresAt: T0 + HOUR,
        now: T0,
      }),
    /maxDepth/,
  );

  const keys = new KeyRegistry();
  keys.generate('root');
  keys.generate('alice');
  const fresh = new DelegationLedger(keys);
  const leafOnly = fresh.issueRoot({
    subject: 'alice',
    resources: ['r'],
    actions: ['read'],
    maxDepth: 0,
    delegatable: true, // flag allows it, but depth budget is exhausted
    notBefore: T0,
    expiresAt: T0 + HOUR,
  });
  assert.throws(
    () =>
      fresh.delegate({
        parentId: leafOnly.id,
        subject: 'bob',
        resources: ['r'],
        actions: ['read'],
        maxDepth: 0,
        delegatable: false,
        notBefore: T0,
        expiresAt: T0 + HOUR,
        now: T0,
      }),
    /no remaining delegation depth/,
  );
});

test('child cannot widen resource or action scope', () => {
  const { ledger, aliceToBob } = makeFixture();
  const attempt = (over: object) =>
    ledger.delegate({
      parentId: aliceToBob.id,
      subject: 'dave',
      resources: ['db/users'],
      actions: ['read'],
      maxDepth: 1,
      delegatable: false,
      notBefore: T0,
      expiresAt: T0 + HOUR,
      now: T0,
      ...over,
    });

  assert.throws(() => attempt({ resources: ['db/users', 'db/audit'] }), /resource scope expansion/);
  assert.throws(() => attempt({ actions: ['read', 'delete'] }), /action scope expansion/);
  assert.throws(() => attempt({ expiresAt: T0 + 6 * HOUR }), /validity window/);
  assert.throws(() => attempt({ notBefore: T0 - 10 }), /validity window/);
});

test('delegation is rejected while the parent is not yet valid or expired', () => {
  const { ledger, rootToAlice } = makeFixture();
  const attempt = (now: number) =>
    ledger.delegate({
      parentId: rootToAlice.id,
      subject: 'dave',
      resources: ['db/users'],
      actions: ['read'],
      maxDepth: 1,
      delegatable: false,
      notBefore: now,
      expiresAt: now + HOUR,
      now,
    });
  assert.throws(() => attempt(T0 - 1), /not valid at issuance/);
  assert.throws(() => attempt(T0 + 10 * HOUR), /not valid at issuance/);
});

test('expired ancestor invalidates a descendant that is still within its own window', () => {
  const keys = new KeyRegistry();
  for (const principal of ['root', 'alice', 'bob']) {
    keys.generate(principal);
  }
  const ledger = new DelegationLedger(keys);
  const short = ledger.issueRoot({
    subject: 'alice',
    resources: ['r1'],
    actions: ['read'],
    maxDepth: 2,
    delegatable: true,
    notBefore: T0,
    expiresAt: T0 + HOUR,
  });
  const child = ledger.delegate({
    parentId: short.id,
    subject: 'bob',
    resources: ['r1'],
    actions: ['read'],
    maxDepth: 1,
    delegatable: false,
    notBefore: T0,
    expiresAt: T0 + HOUR,
    now: T0,
  });

  assert.equal(ledger.verifyChain(child.id, T0 + HOUR / 2).valid, true);
  const proof = ledger.verifyChain(child.id, T0 + 2 * HOUR);
  assert.equal(proof.valid, false);
  assert.match(proof.failure ?? '', new RegExp(`token ${short.id}`));
  assert.equal(
    ledger.authorize({ tokenId: child.id, resource: 'r1', action: 'read', now: T0 + 2 * HOUR })
      .allowed,
    false,
  );
});

test('revoking an ancestor invalidates all descendants', () => {
  const { ledger, rootToAlice, aliceToBob, bobToCarol } = makeFixture();
  const now = T0 + HOUR;
  assert.equal(ledger.verifyChain(bobToCarol.id, now).valid, true);

  ledger.revoke(aliceToBob.id);
  const proof = ledger.verifyChain(bobToCarol.id, now);
  assert.equal(proof.valid, false);
  assert.match(proof.failure ?? '', new RegExp(`token ${aliceToBob.id}`));
  assert.equal(
    ledger.authorize({ tokenId: bobToCarol.id, resource: 'db/users', action: 'read', now }).allowed,
    false,
  );

  // The revoked link itself is also invalid, the sibling root link untouched.
  assert.equal(ledger.verifyChain(aliceToBob.id, now).valid, false);
  assert.equal(ledger.verifyChain(rootToAlice.id, now).valid, true);
});

test('delegation cycles are rejected and can never become a source of power', () => {
  const { ledger, aliceToBob } = makeFixture();
  // alice already appears in the chain root -> alice -> bob.
  assert.throws(
    () =>
      ledger.delegate({
        parentId: aliceToBob.id,
        subject: 'alice',
        resources: ['db/users'],
        actions: ['read'],
        maxDepth: 1,
        delegatable: true,
        notBefore: T0,
        expiresAt: T0 + HOUR,
        now: T0,
      }),
    /cycle/,
  );
  // Self-delegation is a trivial cycle and is rejected too.
  assert.throws(
    () =>
      ledger.delegate({
        parentId: aliceToBob.id,
        subject: 'bob',
        resources: ['db/users'],
        actions: ['read'],
        maxDepth: 1,
        delegatable: true,
        notBefore: T0,
        expiresAt: T0 + HOUR,
        now: T0,
      }),
    /cycle/,
  );
});

test('tampering with a stored token breaks its signature check', () => {
  const { ledger, bobToCarol } = makeFixture();
  const stored = bobToCarol as unknown as { actions: string[] };
  stored.actions = ['read', 'write']; // mutate in place: signature no longer matches

  const proof = ledger.verifyChain(bobToCarol.id, T0 + HOUR);
  assert.equal(proof.valid, false);
  assert.match(proof.failure ?? '', /signature/);
});

test('signatures are issuer-bound and verified against canonical payloads', () => {
  const keys = new KeyRegistry();
  keys.generate('alice');
  keys.generate('bob');
  const payload = canonicalize({ b: 2, a: 1 });
  assert.equal(payload, '{"a":1,"b":2}');
  const signature = keys.sign('alice', payload);
  assert.equal(keys.verify('alice', payload, signature), true);
  assert.equal(keys.verify('bob', payload, signature), false);
  assert.equal(keys.verify('alice', `${payload} `, signature), false);
  assert.equal(keys.verify('mallory', payload, signature), false);
});

test('unknown tokens yield an invalid proof instead of throwing', () => {
  const { ledger } = makeFixture();
  const proof = ledger.verifyChain('no-such-token', T0);
  assert.equal(proof.valid, false);
  assert.match(proof.failure ?? '', /unknown token/);
  assert.throws(() => ledger.revoke('no-such-token'), DelegationError);
});

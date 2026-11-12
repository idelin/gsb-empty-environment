import test from 'node:test';
import assert from 'node:assert/strict';
import { verifyTrustChain } from '../src/verifier.ts';
import { TrustChainError } from '../src/errors.ts';
import { parseCompactJwt } from '../src/jwt.ts';
import type {
  CompactJwt,
  FederationClaims,
  MetadataPolicy,
} from '../src/types.ts';
import {
  buildChain,
  buildKeyring,
  generateEd25519Key,
  inMemoryVerifier,
  signJwt,
  tamperSignature,
  DAY,
  HOUR,
  type EntityKey,
} from './helpers.ts';

const LEAF = 'https://leaf.example';
const INTERMEDIATE = 'https://intermediate.example';
const ANCHOR = 'https://anchor.example';

interface Fixture {
  now: number;
  leafKey: EntityKey;
  intermediateKey: EntityKey;
  anchorKey: EntityKey;
  chain: CompactJwt[];
  verifier: ReturnType<typeof inMemoryVerifier>;
}

function makeFixture(
  overrides: {
    ttlSeconds?: number;
    maxPathLengthByIndex?: number[];
    policyByIndex?: MetadataPolicy[];
  } = {},
): Fixture {
  const now = 1_000_000;
  const leafKey = generateEd25519Key('leaf-key');
  const intermediateKey = generateEd25519Key('intermediate-key');
  const anchorKey = generateEd25519Key('anchor-key');
  const chain = buildChain({
    leafId: LEAF,
    intermediateIds: [INTERMEDIATE],
    anchorId: ANCHOR,
    leafKey,
    intermediateKeys: [intermediateKey],
    anchorKey,
    now,
    ...overrides,
  });
  const keyring = buildKeyring([
    [LEAF, leafKey],
    [INTERMEDIATE, intermediateKey],
    [ANCHOR, anchorKey],
  ]);
  return {
    now,
    leafKey,
    intermediateKey,
    anchorKey,
    chain,
    verifier: inMemoryVerifier(keyring),
  };
}

async function expectCode(
  promise: Promise<unknown>,
  code: string,
): Promise<void> {
  await assert.rejects(promise, (error: unknown) => {
    assert.ok(error instanceof TrustChainError);
    assert.equal(error.code, code);
    return true;
  });
}

test('accepts a well-formed leaf -> intermediate -> anchor chain', async () => {
  const fixture = makeFixture();
  const result = await verifyTrustChain(fixture.chain, {
    verifier: fixture.verifier,
    trustAnchor: ANCHOR,
    leafEntityId: LEAF,
    now: fixture.now * 1000,
  });
  assert.equal(result.leaf, LEAF);
  assert.equal(result.trustAnchor, ANCHOR);
  assert.equal(result.pathLength, 2);
  assert.equal(result.exp, fixture.now + HOUR);
  assert.equal(result.statements.length, 4);
});

test('accepts a direct leaf -> trust anchor chain with no subordinate', async () => {
  const now = 1_000_000;
  const leafKey = generateEd25519Key('leaf-key');
  const anchorKey = generateEd25519Key('anchor-key');
  const chain = buildChain({
    leafId: LEAF,
    intermediateIds: [],
    intermediateKeys: [],
    anchorId: ANCHOR,
    leafKey,
    anchorKey,
    now,
  });
  const keyring = buildKeyring([
    [LEAF, leafKey],
    [ANCHOR, anchorKey],
  ]);
  const result = await verifyTrustChain(chain, {
    verifier: inMemoryVerifier(keyring),
    now: now * 1000,
  });
  assert.equal(result.pathLength, 1);
  assert.equal(result.statements.length, 3);
});

test('rejects a broken chain: forged sub does not match the leaf subject', async () => {
  const fixture = makeFixture();
  const forged = [...fixture.chain];
  const { header, payload } = parseCompactJwt(forged[1]);
  const forgedPayload: FederationClaims = {
    ...payload,
    sub: 'https://attacker.example',
  };
  forged[1] = signJwt(forgedPayload, fixture.intermediateKey, {
    alg: header.alg,
    kid: header.kid,
  });
  await expectCode(
    verifyTrustChain(forged, {
      verifier: fixture.verifier,
      now: fixture.now * 1000,
    }),
    'BROKEN_CHAIN',
  );
});

test('rejects a path longer than max_path_length', async () => {
  const now = 1_000_000;
  const leafKey = generateEd25519Key('leaf-key');
  const keys = [
    generateEd25519Key('ia-key'),
    generateEd25519Key('ib-key'),
    generateEd25519Key('ic-key'),
  ];
  const anchorKey = generateEd25519Key('anchor-key');
  const ids = [
    'https://ia.example',
    'https://ib.example',
    'https://ic.example',
  ];
  // Anchor statement (index 3) only allows one hop below, but two remain.
  const chain = buildChain({
    leafId: LEAF,
    intermediateIds: ids,
    anchorId: ANCHOR,
    leafKey,
    intermediateKeys: keys,
    anchorKey,
    now,
    maxPathLengthByIndex: [2, 1, 0, 1],
  });
  const keyring = buildKeyring([
    [LEAF, leafKey],
    ...ids.map((id, index) => [id, keys[index]] as [string, EntityKey]),
    [ANCHOR, anchorKey],
  ]);
  await expectCode(
    verifyTrustChain(chain, {
      verifier: inMemoryVerifier(keyring),
      now: now * 1000,
    }),
    'PATH_TOO_LONG',
  );
});

test('accepts max_path_length exactly at the boundary', async () => {
  const now = 1_000_000;
  const leafKey = generateEd25519Key('leaf-key');
  const keys = [
    generateEd25519Key('ia-key'),
    generateEd25519Key('ib-key'),
  ];
  const anchorKey = generateEd25519Key('anchor-key');
  const ids = ['https://ia.example', 'https://ib.example'];
  const chain = buildChain({
    leafId: LEAF,
    intermediateIds: ids,
    anchorId: ANCHOR,
    leafKey,
    intermediateKeys: keys,
    anchorKey,
    now,
    // index 2 is the anchor statement; two subordinate statements remain.
    maxPathLengthByIndex: [0, 1, 2],
  });
  const keyring = buildKeyring([
    [LEAF, leafKey],
    ...ids.map((id, index) => [id, keys[index]] as [string, EntityKey]),
    [ANCHOR, anchorKey],
  ]);
  const result = await verifyTrustChain(chain, {
    verifier: inMemoryVerifier(keyring),
    now: now * 1000,
  });
  assert.equal(result.pathLength, 3);
});

test('rejects a loosened metadata_policy (subset_of widens superior set)', async () => {
  const anchorPolicy: MetadataPolicy = {
    openid_relying_party: {
      grant_types: { subset_of: ['authorization_code', 'refresh_token'] },
    },
  };
  const loosenedPolicy: MetadataPolicy = {
    openid_relying_party: {
      grant_types: { subset_of: ['client_credentials'] },
    },
  };
  const fixture = makeFixture({
    policyByIndex: [loosenedPolicy, anchorPolicy],
  });
  await expectCode(
    verifyTrustChain(fixture.chain, {
      verifier: fixture.verifier,
      now: fixture.now * 1000,
    }),
    'POLICY_LOOSENED',
  );
});

test('rejects an expired statement', async () => {
  const fixture = makeFixture({ ttlSeconds: HOUR });
  await expectCode(
    verifyTrustChain(fixture.chain, {
      verifier: fixture.verifier,
      now: (fixture.now + HOUR + 60) * 1000,
    }),
    'EXPIRED',
  );
});

test('rejects a statement that is not yet valid (future iat)', async () => {
  const fixture = makeFixture();
  await expectCode(
    verifyTrustChain(fixture.chain, {
      verifier: fixture.verifier,
      now: (fixture.now - 120) * 1000,
    }),
    'NOT_YET_VALID',
  );
});

test('effective exp is the minimum exp across all statements', async () => {
  const now = 1_000_000;
  const leafKey = generateEd25519Key('leaf-key');
  const intermediateKey = generateEd25519Key('intermediate-key');
  const anchorKey = generateEd25519Key('anchor-key');
  const chain = buildChain({
    leafId: LEAF,
    intermediateIds: [INTERMEDIATE],
    anchorId: ANCHOR,
    leafKey,
    intermediateKeys: [intermediateKey],
    anchorKey,
    now,
    ttlSeconds: DAY,
  });
  const anchorEcIndex = chain.length - 1;
  const parsed = parseCompactJwt(chain[anchorEcIndex]);
  chain[anchorEcIndex] = signJwt(
    { ...parsed.payload, exp: now + 60 },
    anchorKey,
    { alg: parsed.header.alg, kid: parsed.header.kid },
  );
  const keyring = buildKeyring([
    [LEAF, leafKey],
    [INTERMEDIATE, intermediateKey],
    [ANCHOR, anchorKey],
  ]);
  const result = await verifyTrustChain(chain, {
    verifier: inMemoryVerifier(keyring),
    now: now * 1000,
  });
  assert.equal(result.exp, now + 60);
});

test('clock skew keeps an otherwise expired chain acceptable', async () => {
  const fixture = makeFixture({ ttlSeconds: HOUR });
  const result = await verifyTrustChain(fixture.chain, {
    verifier: fixture.verifier,
    now: (fixture.now + HOUR + 30) * 1000,
    clockSkewSeconds: 60,
  });
  assert.equal(result.trustAnchor, ANCHOR);
});

test('rejects a tampered signature via the injected verifier', async () => {
  const fixture = makeFixture();
  const tampered = [...fixture.chain];
  tampered[1] = tamperSignature(tampered[1]);
  await expectCode(
    verifyTrustChain(tampered, {
      verifier: fixture.verifier,
      now: fixture.now * 1000,
    }),
    'INVALID_SIGNATURE',
  );
});

test('rejects self-signed statement in the middle of the chain', async () => {
  const fixture = makeFixture();
  const broken = [...fixture.chain];
  const parsed = parseCompactJwt(broken[1]);
  broken[1] = signJwt(
    { ...parsed.payload, iss: parsed.payload.sub },
    fixture.intermediateKey,
    { alg: parsed.header.alg, kid: parsed.header.kid },
  );
  await expectCode(
    verifyTrustChain(broken, {
      verifier: fixture.verifier,
      now: fixture.now * 1000,
    }),
    'NOT_SUBORDINATE_STATEMENT',
  );
});

test('rejects malformed JWT and empty chain', async () => {
  const fixture = makeFixture();
  await expectCode(
    verifyTrustChain([], {
      verifier: fixture.verifier,
      now: fixture.now * 1000,
    }),
    'EMPTY_CHAIN',
  );
  await expectCode(
    verifyTrustChain(['not-a-jwt', fixture.chain[1]], {
      verifier: fixture.verifier,
      now: fixture.now * 1000,
    }),
    'MALFORMED_JWT',
  );
});

test('rejects trust anchor and leaf identifier mismatches', async () => {
  const fixture = makeFixture();
  await expectCode(
    verifyTrustChain(fixture.chain, {
      verifier: fixture.verifier,
      trustAnchor: 'https://other-anchor.example',
      now: fixture.now * 1000,
    }),
    'TRUST_ANCHOR_MISMATCH',
  );
  await expectCode(
    verifyTrustChain(fixture.chain, {
      verifier: fixture.verifier,
      leafEntityId: 'https://other-leaf.example',
      now: fixture.now * 1000,
    }),
    'LEAF_MISMATCH',
  );
});

test('accepts narrowing policies and returns the intersected subset', async () => {
  const anchorPolicy: MetadataPolicy = {
    openid_relying_party: {
      grant_types: {
        subset_of: ['authorization_code', 'refresh_token', 'implicit'],
      },
    },
  };
  const narrowPolicy: MetadataPolicy = {
    openid_relying_party: {
      grant_types: { subset_of: ['authorization_code', 'refresh_token'] },
    },
  };
  const fixture = makeFixture({
    policyByIndex: [narrowPolicy, anchorPolicy],
  });
  const result = await verifyTrustChain(fixture.chain, {
    verifier: fixture.verifier,
    now: fixture.now * 1000,
  });
  assert.deepEqual(
    result.combinedMetadataPolicy.openid_relying_party.grant_types.subset_of,
    ['authorization_code', 'refresh_token'],
  );
});

test('rejects value that escapes the running subset_of', async () => {
  const anchorPolicy: MetadataPolicy = {
    openid_relying_party: {
      scopes: { subset_of: ['openid', 'profile', 'email'] },
    },
  };
  const badValuePolicy: MetadataPolicy = {
    openid_relying_party: {
      scopes: { value: 'offline_access' },
    },
  };
  const fixture = makeFixture({
    policyByIndex: [badValuePolicy, anchorPolicy],
  });
  await expectCode(
    verifyTrustChain(fixture.chain, {
      verifier: fixture.verifier,
      now: fixture.now * 1000,
    }),
    'POLICY_LOOSENED',
  );
});

test('rejects a broken chain: statement issued by an entity outside the path', async () => {
  const fixture = makeFixture();
  const forged = [...fixture.chain];
  const { header, payload } = parseCompactJwt(forged[1]);
  const attackerKey = generateEd25519Key('attacker-key');
  const forgedPayload: FederationClaims = {
    ...payload,
    iss: 'https://attacker.example',
  };
  forged[1] = signJwt(forgedPayload, attackerKey, {
    alg: header.alg,
    kid: header.kid,
  });
  await expectCode(
    verifyTrustChain(forged, {
      verifier: fixture.verifier,
      now: fixture.now * 1000,
    }),
    'BROKEN_CHAIN',
  );
});

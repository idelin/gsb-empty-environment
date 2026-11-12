import test from 'node:test';
import assert from 'node:assert/strict';
import { verifyTrustChain } from '../src/verifier.ts';
import { TrustChainError } from '../src/errors.ts';
import { parseCompactJwt } from '../src/jwt.ts';
import {
  buildChain,
  generateEd25519Key,
  signJwt,
  HOUR,
} from './helpers.ts';
import { nodeCryptoVerifier } from '../src/crypto-verifier.ts';

const LEAF = 'https://leaf.example';
const INTERMEDIATE = 'https://intermediate.example';
const ANCHOR = 'https://anchor.example';

test('attack: injecting an attacker jwks into a subordinate statement fails signature verification', async () => {
  const now = 3_000_000;
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
  });

  // The attacker rewrites the jwks of the anchor's subordinate statement to
  // their own key, then re-signs only that statement with... nothing valid.
  // Even if they re-signed with their own key, the anchor EC does not carry
  // the attacker's key, so verification against ES[j+1].jwks must fail.
  const attackerKey = generateEd25519Key('attacker-key');
  const targetIndex = 2;
  const parsed = parseCompactJwt(chain[targetIndex]);
  const poisoned = signJwt(
    {
      ...parsed.payload,
      jwks: { keys: [attackerKey.publicJwk] },
    },
    attackerKey,
    { alg: parsed.header.alg, kid: attackerKey.kid },
  );
  chain[targetIndex] = poisoned;

  await assert.rejects(
    verifyTrustChain(chain, {
      verifier: nodeCryptoVerifier,
      now: now * 1000,
    }),
    (error: unknown) => {
      assert.ok(error instanceof TrustChainError);
      assert.equal(error.code, 'INVALID_SIGNATURE');
      return true;
    },
  );
});

test('attack: reordering statements breaks the iss/sub interlock', async () => {
  const now = 3_000_000;
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
  });
  // Swap the two subordinate statements.
  const reordered = [chain[0], chain[2], chain[1], chain[3]];
  await assert.rejects(
    verifyTrustChain(reordered, {
      verifier: nodeCryptoVerifier,
      now: now * 1000,
    }),
    (error: unknown) => {
      assert.ok(error instanceof TrustChainError);
      assert.equal(error.code, 'BROKEN_CHAIN');
      return true;
    },
  );
});

test('attack: a self-signed leaf pretending to be the anchor is rejected', async () => {
  const now = 3_000_000;
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
    ttlSeconds: HOUR,
  });
  // Drop everything but two self-signed configs where the leaf claims to be
  // the anchor; the configured trust anchor must not match.
  const leafParsed = parseCompactJwt(chain[0]);
  const fakeAnchor = signJwt(
    { ...leafParsed.payload, iss: LEAF, sub: LEAF },
    leafKey,
  );
  await assert.rejects(
    verifyTrustChain([chain[0], fakeAnchor], {
      verifier: nodeCryptoVerifier,
      trustAnchor: ANCHOR,
      now: now * 1000,
    }),
    (error: unknown) => {
      assert.ok(error instanceof TrustChainError);
      assert.equal(error.code, 'TRUST_ANCHOR_MISMATCH');
      return true;
    },
  );
});

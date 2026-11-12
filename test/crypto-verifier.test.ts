import test from 'node:test';
import assert from 'node:assert/strict';
import { verifyTrustChain } from '../src/verifier.ts';
import { TrustChainError } from '../src/errors.ts';
import { nodeCryptoVerifier } from '../src/crypto-verifier.ts';
import {
  buildChain,
  generateEd25519Key,
  generateEs256Key,
  generateEs384Key,
  tamperSignature,
  HOUR,
} from './helpers.ts';

const LEAF = 'https://leaf.example';
const INTERMEDIATE = 'https://intermediate.example';
const ANCHOR = 'https://anchor.example';

test('nodeCryptoVerifier accepts a real EdDSA-signed chain', async () => {
  const now = 2_000_000;
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
  const result = await verifyTrustChain(chain, {
    verifier: nodeCryptoVerifier,
    now: now * 1000,
  });
  assert.equal(result.leaf, LEAF);
  assert.equal(result.trustAnchor, ANCHOR);
  assert.equal(result.exp, now + HOUR);
});

test('nodeCryptoVerifier accepts a real ES256-signed chain', async () => {
  const now = 2_000_000;
  const leafKey = generateEs256Key('leaf-key');
  const intermediateKey = generateEs256Key('intermediate-key');
  const anchorKey = generateEs256Key('anchor-key');
  const chain = buildChain({
    leafId: LEAF,
    intermediateIds: [INTERMEDIATE],
    anchorId: ANCHOR,
    leafKey,
    intermediateKeys: [intermediateKey],
    anchorKey,
    now,
  });
  const result = await verifyTrustChain(chain, {
    verifier: nodeCryptoVerifier,
    now: now * 1000,
  });
  assert.equal(result.pathLength, 2);
});

test('nodeCryptoVerifier accepts a real ES384-signed chain', async () => {
  const now = 2_000_000;
  const leafKey = generateEs384Key('leaf-key');
  const intermediateKey = generateEs384Key('intermediate-key');
  const anchorKey = generateEs384Key('anchor-key');
  const chain = buildChain({
    leafId: LEAF,
    intermediateIds: [INTERMEDIATE],
    anchorId: ANCHOR,
    leafKey,
    intermediateKeys: [intermediateKey],
    anchorKey,
    now,
  });
  const result = await verifyTrustChain(chain, {
    verifier: nodeCryptoVerifier,
    now: now * 1000,
  });
  assert.equal(result.trustAnchor, ANCHOR);
  assert.equal(result.exp, now + HOUR);
});

test('nodeCryptoVerifier rejects a tampered EdDSA signature', async () => {
  const now = 2_000_000;
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
  chain[0] = tamperSignature(chain[0]);
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

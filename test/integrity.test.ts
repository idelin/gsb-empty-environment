import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  ContentAddressedCache,
  IntegrityError,
  computeDigest,
  fetchVerifiedResource,
  generateIntegrity,
  parseIntegrity,
  verifyIntegrity,
} from '../index.ts';

const serverContent = {
  safe: 'export const value = "trusted";\n',
  replaced: 'export const value = "cdn-replaced";\n',
};

const sameOrigin = 'https://app.example/';
const crossOrigin = 'https://cdn.example/';

function mockResponse(
  body: string,
  url: string,
  redirected = false,
): Response {
  const response = new Response(body, {
    status: 200,
    headers: { 'content-type': 'text/javascript; charset=utf-8' },
  });
  Object.defineProperties(response, {
    url: { value: url },
    redirected: { value: redirected },
  });
  return response;
}

async function sri(
  content: string,
  algorithm: 'sha1' | 'sha256' | 'sha384' | 'sha512',
): Promise<string> {
  return `${algorithm}-${await computeDigest(content, algorithm)}`;
}

test('rejects a SHA-1 fallback when the strong digest does not match', async () => {
  const integrity = [
    await sri('attacker-controlled', 'sha256'),
    await sri(serverContent.safe, 'sha1'),
  ].join(' ');

  await assert.rejects(
    verifyIntegrity(serverContent.safe, integrity),
    (error: unknown) =>
      error instanceof IntegrityError &&
      error.code === 'INTEGRITY_MISMATCH' &&
      error.algorithm === 'sha256',
  );
});

test('never accepts a SHA-1-only declaration, even with a lowered policy', async () => {
  const weak = await sri(serverContent.safe, 'sha1');

  await assert.rejects(
    verifyIntegrity(serverContent.safe, weak, { minimumStrength: 1 }),
    (error: unknown) =>
      error instanceof IntegrityError && error.code === 'WEAK_ALGORITHM',
  );

  let fetchCalled = false;
  await assert.rejects(
    fetchVerifiedResource('https://cdn.example/legacy.js', {
      integrity: weak,
      highRisk: true,
      fetch: async () => {
        fetchCalled = true;
        return mockResponse(serverContent.safe, 'https://cdn.example/legacy.js');
      },
    }),
    (error: unknown) =>
      error instanceof IntegrityError && error.code === 'WEAK_ALGORITHM',
  );
  assert.equal(fetchCalled, false);
});

test('uses only the strongest algorithm when multiple digests conflict', async () => {
  const parsed = parseIntegrity(
    [
      await sri(serverContent.safe, 'sha1'),
      await sri('attacker-controlled', 'sha256'),
      await sri(serverContent.safe, 'sha512'),
    ].join(' '),
  );

  assert.equal(parsed.strongestAlgorithm, 'sha512');
  assert.deepEqual(
    parsed.hashes.map((hash) => hash.algorithm),
    ['sha512', 'sha256', 'sha1'],
  );
  const result = await verifyIntegrity(serverContent.safe, parsed.raw);
  assert.equal(result.algorithm, 'sha512');

  const conflicting = [
    await sri(serverContent.safe, 'sha1'),
    await sri('attacker-controlled', 'sha256'),
  ].join(' ');
  await assert.rejects(
    verifyIntegrity(serverContent.safe, conflicting),
    (error: unknown) =>
      error instanceof IntegrityError &&
      error.code === 'INTEGRITY_MISMATCH',
  );
});

test('checks the final body after a redirect instead of trusting the origin', async () => {
  const expected = await sri(serverContent.safe, 'sha384');
  const finalUrl = `${crossOrigin}replaced.js`;

  await assert.rejects(
    fetchVerifiedResource(`${sameOrigin}redirect.js`, {
      integrity: expected,
      highRisk: true,
      fetch: async () =>
        mockResponse(serverContent.replaced, finalUrl, true),
    }),
    (error: unknown) =>
      error instanceof IntegrityError &&
      error.code === 'INTEGRITY_MISMATCH' &&
      error.algorithm === 'sha384',
  );

  const replacementIntegrity = await sri(serverContent.replaced, 'sha384');
  const accepted = await fetchVerifiedResource(`${sameOrigin}redirect.js`, {
    integrity: replacementIntegrity,
    highRisk: true,
    baseUrl: sameOrigin,
    fetch: async () => mockResponse(serverContent.replaced, finalUrl, true),
  });
  assert.equal(accepted.finalUrl, finalUrl);
  assert.equal(accepted.redirected, true);
  assert.equal(accepted.crossOrigin, true);
  assert.equal(accepted.text(), serverContent.replaced);
});

test('rejects a high-risk resource with no integrity attribute', async () => {
  let fetchCalled = false;
  await assert.rejects(
    fetchVerifiedResource('https://cdn.example/high-risk.js', {
      fetch: async () => {
        fetchCalled = true;
        return new Response(serverContent.safe);
      },
    }),
    (error: unknown) =>
      error instanceof IntegrityError &&
      error.code === 'MISSING_INTEGRITY',
  );
  assert.equal(fetchCalled, false);
});

test('accepts a matching strong digest for same-origin and cross-origin loads', async () => {
  const sameOriginIntegrity = await sri(serverContent.safe, 'sha256');
  const sameOriginResult = await fetchVerifiedResource(
    `${sameOrigin}safe.js`,
    {
      integrity: sameOriginIntegrity,
      highRisk: true,
      baseUrl: sameOrigin,
      fetch: async () =>
        mockResponse(serverContent.safe, `${sameOrigin}safe.js`),
    },
  );
  assert.equal(sameOriginResult.text(), serverContent.safe);
  assert.equal(sameOriginResult.crossOrigin, false);
  assert.equal(sameOriginResult.algorithm, 'sha256');

  const crossOriginIntegrity = await sri(serverContent.safe, 'sha384');
  const crossOriginResult = await fetchVerifiedResource(
    `${crossOrigin}safe.js`,
    {
      integrity: crossOriginIntegrity,
      highRisk: true,
      baseUrl: sameOrigin,
      fetch: async () =>
        mockResponse(serverContent.safe, `${crossOrigin}safe.js`),
    },
  );
  assert.equal(crossOriginResult.text(), serverContent.safe);
  assert.equal(crossOriginResult.crossOrigin, true);
  assert.equal(crossOriginResult.algorithm, 'sha384');
});

test('hits the content-addressed cache without another network read', async () => {
  const integrity = await sri(serverContent.safe, 'sha256');
  const cache = new ContentAddressedCache();
  let requests = 0;

  const first = await fetchVerifiedResource(`${sameOrigin}safe.js`, {
    integrity,
    cache,
    highRisk: true,
    fetch: async () => {
      requests += 1;
      return mockResponse(serverContent.safe, `${sameOrigin}safe.js`);
    },
  });
  assert.equal(first.cacheHit, false);
  assert.match(first.contentKey, /^sha256-/);
  assert.equal(requests, 1);

  const requestsBeforeHit = requests;
  const second = await fetchVerifiedResource(`${sameOrigin}other.js`, {
    integrity,
    cache,
    highRisk: true,
    fetch: async () => {
      throw new Error('network must not be used on a digest-keyed cache hit');
    },
  });
  assert.equal(requests, requestsBeforeHit);
  assert.equal(second.cacheHit, true);
  assert.equal(second.contentKey, first.contentKey);
  assert.equal(second.text(), serverContent.safe);
  assert.equal(
    cache.getByDigest(first.algorithm, first.digest)?.key,
    first.contentKey,
  );
});

test('build generation never emits SHA-1', async () => {
  const mixed = await generateIntegrity(serverContent.safe, [
    'sha1',
    'sha256',
  ]);
  assert.match(mixed, /^sha256-/);
  assert.doesNotMatch(mixed, /sha1-/);

  await assert.rejects(
    generateIntegrity(serverContent.safe, ['sha1']),
    (error: unknown) =>
      error instanceof IntegrityError && error.code === 'WEAK_ALGORITHM',
  );
});

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { decideCookieCarry } from '../src/index.ts';
import { ancestorChain, buildHarness, cookie, httpOrigin, request, signedBundle } from './harness.ts';

test('重定向目标缺少主机证明时返回 invalid-rules-proof 并全量拒绝', () => {
  const harness = buildHarness();
  // 只证明初始主机，重定向目标 evil.test 无证明。
  const bundle = signedBundle(harness, ['shop.example'], 900);
  const req = request({
    url: 'https://shop.example/start',
    method: 'GET',
    ancestorChain: ancestorChain(httpOrigin('shop.example')),
    redirects: [{ status: 307, location: 'https://evil.test/target', atMs: 100 }],
  });
  const decision = decideCookieCarry({
    request: req,
    cookies: [
      cookie({ name: 'a', domain: 'test' }),
      cookie({ name: 'b', domain: 'example' }),
    ],
    trustAnchor: harness.anchor,
    proofBundle: bundle,
    nowMs: 200,
  });
  assert.equal(decision.rulesVerification.accepted, false);
  assert.equal(decision.rulesVerification.reason, 'invalid-rules-proof');
  assert.deepEqual(decision.cookies.map((entry) => entry.send), [false, false]);
});

test('证明束缺少候选键时核验失败', () => {
  const harness = buildHarness();
  const full = signedBundle(harness, ['shop.example'], 900);
  const hostProof = full.hostProofs[0];
  const firstKey = Object.keys(hostProof.proofs)[0];
  const partialProofs = { ...hostProof.proofs };
  delete partialProofs[firstKey];
  const partialBundle = {
    ...full,
    hostProofs: [{ ...hostProof, proofs: partialProofs }],
  };
  const req = request({
    url: 'https://shop.example/',
    method: 'GET',
    ancestorChain: ancestorChain(httpOrigin('shop.example')),
  });
  const decision = decideCookieCarry({
    request: req,
    cookies: [cookie({ name: 'sid', domain: 'example' })],
    trustAnchor: harness.anchor,
    proofBundle: partialBundle,
    nowMs: 200,
  });
  assert.equal(decision.rulesVerification.reason, 'invalid-rules-proof');
  assert.equal(decision.cookies[0].send, false);
});

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { decideCookieCarry, decisionFingerprint, recomputeReference } from '../src/index.ts';
import { ancestorChain, buildHarness, cookie, httpOrigin, request, signedBundle } from './harness.ts';

test('无缓存重算：不同时刻独立计算得到逐字段一致结论', () => {
  const harness = buildHarness();
  const bundle = signedBundle(harness, ['shop.example', 'evil.test'], 900);
  const req = request({
    url: 'https://shop.example/start',
    method: 'POST',
    initiatedAtMs: 1_000_000,
    ancestorChain: ancestorChain(httpOrigin('shop.example')),
    redirects: [
      { status: 307, location: 'https://evil.test/handoff', atMs: 1_000_100 },
      { status: 303, location: 'https://shop.example/return', atMs: 1_000_200 },
    ],
  });
  const cookies = [
    cookie({ name: 'strict-c', domain: 'example', sameSite: 'strict' }),
    cookie({ name: 'lax-c', domain: 'example', sameSite: 'lax', createdAtMs: 999_500 }),
  ];
  const context = {
    request: req,
    cookies,
    trustAnchor: harness.anchor,
    proofBundle: bundle,
    nowMs: 1_000_300,
  };
  const check = recomputeReference(context, 9_999_999);
  assert.equal(check.consistent, true);
  assert.notEqual(check.first.computedAtMs, check.second.computedAtMs);
  assert.equal(decisionFingerprint(check.first), check.fingerprint);
  assert.deepEqual(
    check.first.cookies.map((entry) => [entry.name, entry.send, entry.reason]),
    check.second.cookies.map((entry) => [entry.name, entry.send, entry.reason]),
  );
});

test('无缓存重算对缺失外部根拒绝同样稳定', () => {
  const req = request({
    url: 'https://shop.example/x',
    method: 'GET',
    ancestorChain: ancestorChain(httpOrigin('shop.example')),
  });
  const context = {
    request: req,
    cookies: [cookie({ name: 'sid', domain: 'example' })],
    trustAnchor: null,
    proofBundle: null,
    nowMs: 1,
  };
  const check = recomputeReference(context, 2);
  assert.equal(check.consistent, true);
  assert.equal(check.first.rulesVerification.reason, 'missing-external-root');
  assert.equal(check.second.rulesVerification.reason, 'missing-external-root');
});

test('PSL 精确、通配与例外规则经证明核验后正确求值', () => {
  const harness = buildHarness();
  const cases: Array<[string, string, string]> = [
    ['shop.example', 'example', 'default'],
    ['a.github.io', 'a.github.io', 'exact'],
    ['x.ck', 'x.ck', 'wildcard'],
    ['www.ck', 'www.ck', 'exception'],
    ['api.co.uk', 'api.co.uk', 'exact'],
  ];
  for (const [host, expectedDomain, expectedType] of cases) {
    const bundle = signedBundle(harness, [host], 900);
    const req = request({
      url: `https://${host}/`,
      method: 'GET',
      ancestorChain: ancestorChain(httpOrigin(host)),
    });
    const decision = decideCookieCarry({
      request: req,
      cookies: [cookie({ name: 'sid', domain: expectedDomain })],
      trustAnchor: harness.anchor,
      proofBundle: bundle,
      nowMs: 1000,
    });
    assert.equal(decision.rulesVerification.accepted, true, host);
    assert.equal(decision.rulesVerification.matchedRuleType, expectedType, host);
    assert.equal(decision.cookies[0].send, true, host);
  }
});

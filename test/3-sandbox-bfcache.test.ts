import assert from 'node:assert/strict';
import { test } from 'node:test';
import { BFCacheStore, decideCookieCarry } from '../src/index.ts';
import {
  ancestorChain,
  buildHarness,
  cookie,
  frameTree,
  httpOrigin,
  opaqueOrigin,
  request,
  signedBundle,
} from './harness.ts';

test('sandbox 且无 allow-same-origin：不透明身份导致跨站，Strict 被拦截', () => {
  const harness = buildHarness();
  const bundle = signedBundle(harness, ['shop.example'], 900);
  const chain = ancestorChain(
    httpOrigin('shop.example'),
    { origin: opaqueOrigin('nonce-iframe-1'), sandboxed: true, allowSameOrigin: false },
  );
  const req = request({
    url: 'https://shop.example/api',
    method: 'GET',
    topLevelNavigation: false,
    ancestorChain: chain,
  });
  const decision = decideCookieCarry({
    request: req,
    cookies: [
      cookie({ name: 'strict-c', domain: 'example', sameSite: 'strict' }),
      cookie({ name: 'none-c', domain: 'example', sameSite: 'none' }),
    ],
    trustAnchor: harness.anchor,
    proofBundle: bundle,
    nowMs: 1_000_200,
  });
  assert.equal(decision.crossedSiteLatch, true);
  assert.equal(decision.cookies[0].reason, 'same-site-strict-blocked');
  assert.equal(decision.cookies[1].reason, 'cross-site-redirect-latch');
});

test('sandbox 切换：allow-same-origin 保留真实来源，同站判定恢复', () => {
  const harness = buildHarness();
  const bundle = signedBundle(harness, ['shop.example'], 900);
  const chain = ancestorChain(
    httpOrigin('shop.example'),
    { origin: httpOrigin('shop.example'), sandboxed: true, allowSameOrigin: true },
  );
  const req = request({
    url: 'https://shop.example/api',
    method: 'GET',
    topLevelNavigation: false,
    ancestorChain: chain,
  });
  const decision = decideCookieCarry({
    request: req,
    cookies: [cookie({ name: 'strict-c', domain: 'example', sameSite: 'strict' })],
    trustAnchor: harness.anchor,
    proofBundle: bundle,
    nowMs: 1_000_200,
  });
  assert.equal(decision.sameSiteContext, true);
  assert.equal(decision.cookies[0].send, true);
});

test('BFCache 恢复原始冻结上下文，在途请求不受暂定导航与策略升版影响', () => {
  const harness = buildHarness('policy-v2');
  const bundle = signedBundle(harness, ['shop.example', 'evil.test'], 900, 'policy-v1');
  const store = new BFCacheStore();

  const frozenTree = frameTree(
    ...ancestorChain(httpOrigin('shop.example'), {
      origin: opaqueOrigin('nonce-frozen'),
      sandboxed: true,
      allowSameOrigin: false,
    }),
  );
  const inFlight = request({
    url: 'https://shop.example/in-flight',
    method: 'POST',
    policyVersion: 'policy-v1',
    topLevelNavigation: false,
    initiatedAtMs: 5_000,
    ancestorChain: ancestorChain(httpOrigin('shop.example'), {
      origin: opaqueOrigin('nonce-frozen'),
      sandboxed: true,
      allowSameOrigin: false,
    }),
  });

  const frozen = store.freeze('page-1', frozenTree, [inFlight], 6_000);
  Object.isFrozen(frozen);

  // 暂定导航把宿主策略升到 v2；恢复后在途请求仍以 v1 快照重算。
  const restored = store.restore('page-1', 8_000);
  assert.equal(restored.policyVersionAtFreeze, 'policy-v1');
  assert.equal(restored.inFlightRequests[0].policyVersion, 'policy-v1');
  assert.equal(restored.contextTree.children[0].effectiveOrigin.opaque, true);

  const decision = decideCookieCarry({
    request: restored.inFlightRequests[0],
    cookies: [cookie({ name: 'sid', domain: 'example', sameSite: 'strict' })],
    trustAnchor: harness.anchor,
    proofBundle: bundle,
    nowMs: 9_000,
  });
  assert.equal(decision.policyVersion, 'policy-v1');
  assert.equal(decision.crossedSiteLatch, true);
  assert.equal(decision.cookies[0].reason, 'same-site-strict-blocked');
});

test('策略版本与受签根不匹配时拒绝（升版不得作用于在途请求）', () => {
  const harness = buildHarness('policy-v2');
  const bundle = signedBundle(harness, ['shop.example'], 900, 'policy-v2');
  const req = request({
    url: 'https://shop.example/x',
    method: 'GET',
    policyVersion: 'policy-v1',
    ancestorChain: ancestorChain(httpOrigin('shop.example')),
  });
  const decision = decideCookieCarry({
    request: req,
    cookies: [cookie({ name: 'sid', domain: 'example' })],
    trustAnchor: harness.anchor,
    proofBundle: bundle,
    nowMs: 1_000,
  });
  assert.equal(decision.rulesVerification.accepted, false);
  assert.equal(decision.rulesVerification.reason, 'untrusted-rules-root');
  assert.equal(decision.cookies[0].send, false);
});

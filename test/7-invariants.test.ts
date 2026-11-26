import assert from 'node:assert/strict';
import { test } from 'node:test';
import { ancestorChainFromTree, decideCookieCarry, type FrameNode } from '../src/index.ts';
import { ancestorChain, buildHarness, cookie, frameTree, httpOrigin, opaqueOrigin, request, signedBundle } from './harness.ts';

test('sandbox 无 allow-same-origin 却带真实来源：上下文树不变量校验抛错', () => {
  const invalidTree: FrameNode = {
    id: 'top',
    effectiveOrigin: httpOrigin('shop.example'),
    sandboxed: true,
    allowSameOrigin: false,
    children: [],
  };
  assert.throws(() => ancestorChainFromTree(invalidTree, 'top'));
});

test('ancestorChainFromTree 抽取的祖先链与直接构造一致', () => {
  const chain = ancestorChain(httpOrigin('portal.test'), httpOrigin('widget.example'));
  const tree = frameTree(...chain);
  const extracted = ancestorChainFromTree(tree, 'frame-1');
  assert.equal(extracted.length, 2);
  assert.equal(extracted[1].frameId, 'frame-1');
  assert.deepEqual(extracted[0].effectiveOrigin, httpOrigin('portal.test'));
});

test('目标框架不在树中时抛错', () => {
  const tree = frameTree(...ancestorChain(httpOrigin('shop.example')));
  assert.throws(() => ancestorChainFromTree(tree, 'missing'));
});

test('域与路径不匹配在跨站判定前短路', () => {
  const harness = buildHarness();
  const bundle = signedBundle(harness, ['shop.example'], 900);
  const req = request({
    url: 'https://shop.example/order/checkout',
    method: 'GET',
    ancestorChain: ancestorChain(httpOrigin('shop.example')),
  });
  const decision = decideCookieCarry({
    request: req,
    cookies: [
      cookie({ name: 'other-domain', domain: 'other.test' }),
      cookie({ name: 'other-path', domain: 'example', path: '/account' }),
      cookie({ name: 'ok', domain: 'example', path: '/order' }),
    ],
    trustAnchor: harness.anchor,
    proofBundle: bundle,
    nowMs: 1000,
  });
  const byName = Object.fromEntries(decision.cookies.map((entry) => [entry.name, entry]));
  assert.equal(byName['other-domain'].reason, 'domain-mismatch');
  assert.equal(byName['other-path'].reason, 'path-mismatch');
  assert.equal(byName['ok'].send, true);
});

test('不透明身份之间即使标签不同也恒为跨站', () => {
  const harness = buildHarness();
  const bundle = signedBundle(harness, ['widget.example'], 900);
  const req = request({
    url: 'https://widget.example/api',
    method: 'GET',
    topLevelNavigation: false,
    ancestorChain: ancestorChain(
      { origin: opaqueOrigin('nonce-a'), sandboxed: true, allowSameOrigin: false },
      { origin: opaqueOrigin('nonce-b'), sandboxed: true, allowSameOrigin: false },
    ),
  });
  const decision = decideCookieCarry({
    request: req,
    cookies: [cookie({ name: 'strict-c', domain: 'example', sameSite: 'strict' })],
    trustAnchor: harness.anchor,
    proofBundle: bundle,
    nowMs: 1000,
  });
  assert.equal(decision.crossedSiteLatch, true);
  assert.equal(decision.partitionKey, null);
  assert.equal(decision.cookies[0].reason, 'same-site-strict-blocked');
});

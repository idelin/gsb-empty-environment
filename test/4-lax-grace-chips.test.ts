import assert from 'node:assert/strict';
import { test } from 'node:test';
import { decideCookieCarry, LAX_GRACE_WINDOW_MS } from '../src/index.ts';
import { ancestorChain, buildHarness, cookie, httpOrigin, request, signedBundle, site } from './harness.ts';

function crossSitePost(createdAtMs: number, nowMs: number) {
  const harness = buildHarness();
  const bundle = signedBundle(harness, ['shop.example', 'referrer.test'], 900);
  const req = request({
    url: 'https://shop.example/pay',
    method: 'POST',
    initiatedAtMs: nowMs,
    ancestorChain: ancestorChain(httpOrigin('referrer.test')),
  });
  return decideCookieCarry({
    request: req,
    cookies: [cookie({ name: 'fresh-lax', domain: 'example', sameSite: 'lax', createdAtMs })],
    trustAnchor: harness.anchor,
    proofBundle: bundle,
    nowMs,
  });
}

test('LAX-allow-unsafe 宽限边界：120000ms 硬切点', () => {
  const now = 10_000_000;
  const inside = crossSitePost(now - (LAX_GRACE_WINDOW_MS - 1), now);
  assert.equal(inside.cookies[0].send, true);

  const atBoundary = crossSitePost(now - LAX_GRACE_WINDOW_MS, now);
  assert.equal(atBoundary.cookies[0].send, false);
  assert.equal(atBoundary.cookies[0].reason, 'lax-grace-expired');

  const outside = crossSitePost(now - (LAX_GRACE_WINDOW_MS + 1), now);
  assert.equal(outside.cookies[0].send, false);
  assert.equal(outside.cookies[0].reason, 'lax-grace-expired');
});

test('跨站 GET 顶层导航：Lax 安全方法直接放行，与年龄无关', () => {
  const harness = buildHarness();
  const bundle = signedBundle(harness, ['shop.example', 'evil.test'], 900);
  const req = request({
    url: 'https://shop.example/',
    method: 'GET',
    initiatedAtMs: 5_000_000,
    ancestorChain: ancestorChain(httpOrigin('evil.test')),
  });
  const decision = decideCookieCarry({
    request: req,
    cookies: [cookie({ name: 'old-lax', domain: 'example', createdAtMs: 0 })],
    trustAnchor: harness.anchor,
    proofBundle: bundle,
    nowMs: 5_000_000,
  });
  assert.equal(decision.laxNavigationEligible, true);
  assert.equal(decision.cookies[0].send, true);
});

test('CHIPS 分区 Cookie 按顶层站点分区键绑定', () => {
  const harness = buildHarness();
  const bundle = signedBundle(harness, ['widget.example', 'portal.test'], 900);
  const req = request({
    url: 'https://widget.example/embed',
    method: 'GET',
    topLevelNavigation: false,
    ancestorChain: ancestorChain(httpOrigin('portal.test'), httpOrigin('widget.example')),
  });
  const matchingKey = site('https', 'portal.test');
  const otherKey = site('https', 'other.test');
  const decision = decideCookieCarry({
    request: req,
    cookies: [
      cookie({
        name: 'partitioned-ok',
        domain: 'example',
        sameSite: 'none',
        partitioned: true,
        partitionKey: matchingKey,
      }),
      cookie({
        name: 'partitioned-bad',
        domain: 'example',
        sameSite: 'none',
        partitioned: true,
        partitionKey: otherKey,
      }),
    ],
    trustAnchor: harness.anchor,
    proofBundle: bundle,
    nowMs: 2000,
  });
  const byName = Object.fromEntries(decision.cookies.map((entry) => [entry.name, entry]));
  assert.deepEqual(decision.partitionKey, matchingKey);
  assert.equal(byName['partitioned-ok'].send, true);
  assert.equal(byName['partitioned-bad'].reason, 'partition-key-mismatch');
});

test('不透明顶层分区下分区 Cookie 拒绝', () => {
  const harness = buildHarness();
  const bundle = signedBundle(harness, ['widget.example'], 900);
  const req = request({
    url: 'https://widget.example/embed',
    method: 'GET',
    topLevelNavigation: false,
    ancestorChain: ancestorChain(
      { origin: { opaque: true, identityTag: 'nonce-top' }, sandboxed: true, allowSameOrigin: false },
      httpOrigin('widget.example'),
    ),
  });
  const decision = decideCookieCarry({
    request: req,
    cookies: [
      cookie({
        name: 'partitioned-c',
        domain: 'example',
        sameSite: 'none',
        partitioned: true,
        partitionKey: site('https', 'x.test'),
      }),
    ],
    trustAnchor: harness.anchor,
    proofBundle: bundle,
    nowMs: 2000,
  });
  assert.equal(decision.partitionKey, null);
  assert.equal(decision.cookies[0].reason, 'opaque-partition');
});

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { decideCookieCarry } from '../src/index.ts';
import { ancestorChain, buildHarness, cookie, httpOrigin, request, signedBundle } from './harness.ts';

test('303 把跨站链洗回同站：闩锁不解除，Lax 资格不重新激活', () => {
  const harness = buildHarness();
  const hosts = ['shop.example', 'evil.test', 'shop.example'];
  const bundle = signedBundle(harness, hosts, 900);
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
    cookie({ name: 'lax-c', domain: 'example', sameSite: 'lax', createdAtMs: 1_000_000 - 1000 }),
    cookie({ name: 'none-c', domain: 'example', sameSite: 'none', secure: true }),
  ];
  const decision = decideCookieCarry({
    request: req,
    cookies,
    trustAnchor: harness.anchor,
    proofBundle: bundle,
    nowMs: 1_000_300,
  });

  assert.equal(decision.effectiveMethod, 'GET');
  assert.equal(decision.crossedSiteLatch, true);
  assert.equal(decision.sameSiteContext, false);
  assert.equal(decision.laxNavigationEligible, false);
  assert.equal(decision.finalUrl, 'https://shop.example/return');
  const byName = Object.fromEntries(decision.cookies.map((entry) => [entry.name, entry]));
  assert.equal(byName['strict-c'].send, false);
  assert.equal(byName['strict-c'].reason, 'same-site-strict-blocked');
  assert.equal(byName['lax-c'].send, false);
  assert.equal(byName['lax-c'].reason, '303-reactivation-denied');
  assert.equal(byName['none-c'].send, false);
  assert.equal(byName['none-c'].reason, 'cross-site-redirect-latch');
});

test('307/308 保留方法，跨站后 Lax 不安全方法仍被拦截', () => {
  const harness = buildHarness();
  const bundle = signedBundle(harness, ['shop.example', 'evil.test'], 900);
  const req = request({
    url: 'https://shop.example/pay',
    method: 'POST',
    initiatedAtMs: 1_000_000,
    ancestorChain: ancestorChain(httpOrigin('shop.example')),
    redirects: [{ status: 307, location: 'https://evil.test/pay', atMs: 1_000_100 }],
  });
  const decision = decideCookieCarry({
    request: req,
    cookies: [cookie({ name: 'lax-c', domain: 'test', createdAtMs: 1_000_000 - 200_000 })],
    trustAnchor: harness.anchor,
    proofBundle: bundle,
    nowMs: 1_000_200,
  });
  assert.equal(decision.effectiveMethod, 'POST');
  assert.equal(decision.crossedSiteLatch, true);
  assert.equal(decision.cookies[0].send, false);
  assert.equal(decision.cookies[0].reason, 'lax-grace-expired');
});

test('Schemeful：http 与 https 之间视为跨站，即使注册域相同', () => {
  const harness = buildHarness();
  const bundle = signedBundle(harness, ['shop.example'], 900);
  const req = request({
    url: 'http://shop.example/start',
    method: 'GET',
    ancestorChain: ancestorChain(httpOrigin('shop.example', 'http')),
    redirects: [{ status: 307, location: 'https://shop.example/secure', atMs: 1_000_100 }],
  });
  const decision = decideCookieCarry({
    request: req,
    cookies: [
      cookie({ name: 'strict-c', domain: 'example', sameSite: 'strict' }),
      cookie({ name: 'lax-c', domain: 'example', sameSite: 'lax' }),
    ],
    trustAnchor: harness.anchor,
    proofBundle: bundle,
    nowMs: 1_000_200,
  });
  assert.equal(decision.crossedSiteLatch, true);
  assert.equal(decision.trace[1].crossSite, true);
  const byName = Object.fromEntries(decision.cookies.map((entry) => [entry.name, entry]));
  assert.equal(byName['strict-c'].reason, 'same-site-strict-blocked');
  assert.equal(byName['lax-c'].send, true);
});

test('同站 303 不触发闩锁，Cookie 正常携带', () => {
  const harness = buildHarness();
  const bundle = signedBundle(harness, ['shop.example'], 900);
  const req = request({
    url: 'https://shop.example/form',
    method: 'POST',
    ancestorChain: ancestorChain(httpOrigin('shop.example')),
    redirects: [{ status: 303, location: 'https://shop.example/done', atMs: 1_000_100 }],
  });
  const decision = decideCookieCarry({
    request: req,
    cookies: [cookie({ name: 'sid', domain: 'example' })],
    trustAnchor: harness.anchor,
    proofBundle: bundle,
    nowMs: 1_000_200,
  });
  assert.equal(decision.crossedSiteLatch, false);
  assert.equal(decision.sameSiteContext, true);
  assert.equal(decision.cookies[0].send, true);
});

test('跨站子框架 POST 非顶层导航：新鲜 Lax Cookie 也不享受宽限', () => {
  const harness = buildHarness();
  const now = 2_000_000;
  const bundle = signedBundle(harness, ['widget.example', 'portal.test'], 900);
  const req = request({
    url: 'https://widget.example/api',
    method: 'POST',
    initiatedAtMs: now,
    topLevelNavigation: false,
    ancestorChain: ancestorChain(httpOrigin('portal.test'), httpOrigin('widget.example')),
    redirects: [],
  });
  const decision = decideCookieCarry({
    request: req,
    cookies: [cookie({ name: 'lax-c', domain: 'example', sameSite: 'lax', createdAtMs: now - 1000 })],
    trustAnchor: harness.anchor,
    proofBundle: bundle,
    nowMs: now,
  });
  assert.equal(decision.crossedSiteLatch, true);
  assert.equal(decision.laxUnsafeGraceEligible, false);
  assert.equal(decision.cookies[0].send, false);
  assert.equal(decision.cookies[0].reason, 'lax-unsafe-method-blocked');
});

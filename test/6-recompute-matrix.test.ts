import assert from 'node:assert/strict';
import { test } from 'node:test';
import { LAX_GRACE_WINDOW_MS, recomputeReference, type DecisionContext } from '../src/index.ts';
import { ancestorChain, buildHarness, cookie, httpOrigin, request, signedBundle } from './harness.ts';

test('无缓存重算矩阵：303 洗白 / 跨协议 / sandbox / 宽限临界结论稳定', () => {
  const scenarios: Array<{ name: string; build: () => DecisionContext }> = [
    {
      name: '303-洗白拦截',
      build: () => {
        const harness = buildHarness();
        const bundle = signedBundle(harness, ['shop.example', 'evil.test'], 900);
        return {
          request: request({
            url: 'https://shop.example/start',
            method: 'POST',
            ancestorChain: ancestorChain(httpOrigin('shop.example')),
            redirects: [
              { status: 307, location: 'https://evil.test/handoff', atMs: 100 },
              { status: 303, location: 'https://shop.example/back', atMs: 200 },
            ],
          }),
          cookies: [
            cookie({ name: 'lax-c', domain: 'example', sameSite: 'lax', createdAtMs: 999_000 }),
            cookie({ name: 'strict-c', domain: 'example', sameSite: 'strict' }),
          ],
          trustAnchor: harness.anchor,
          proofBundle: bundle,
          nowMs: 1_000,
        };
      },
    },
    {
      name: '跨协议降级',
      build: () => {
        const harness = buildHarness();
        const bundle = signedBundle(harness, ['shop.example'], 900);
        return {
          request: request({
            url: 'http://shop.example/start',
            method: 'GET',
            ancestorChain: ancestorChain(httpOrigin('shop.example', 'http')),
            redirects: [{ status: 307, location: 'https://shop.example/secure', atMs: 100 }],
          }),
          cookies: [cookie({ name: 'strict-c', domain: 'example', sameSite: 'strict' })],
          trustAnchor: harness.anchor,
          proofBundle: bundle,
          nowMs: 1_000,
        };
      },
    },
    {
      name: 'sandbox 切换',
      build: () => {
        const harness = buildHarness();
        const bundle = signedBundle(harness, ['shop.example'], 900);
        return {
          request: request({
            url: 'https://shop.example/api',
            method: 'GET',
            topLevelNavigation: false,
            ancestorChain: ancestorChain(
              httpOrigin('shop.example'),
              { origin: { opaque: true, identityTag: 'nonce-s' }, sandboxed: true, allowSameOrigin: false },
            ),
          }),
          cookies: [cookie({ name: 'strict-c', domain: 'example', sameSite: 'strict' })],
          trustAnchor: harness.anchor,
          proofBundle: bundle,
          nowMs: 1_000,
        };
      },
    },
    {
      name: 'Lax 宽限临界（边界外 1ms）',
      build: () => {
        const now = 7_000_000;
        const harness = buildHarness();
        const bundle = signedBundle(harness, ['shop.example', 'referrer.test'], 900);
        return {
          request: request({
            url: 'https://shop.example/pay',
            method: 'POST',
            initiatedAtMs: now,
            ancestorChain: ancestorChain(httpOrigin('referrer.test')),
          }),
          cookies: [
            cookie({
              name: 'lax-c',
              domain: 'example',
              sameSite: 'lax',
              createdAtMs: now - LAX_GRACE_WINDOW_MS - 1,
            }),
          ],
          trustAnchor: harness.anchor,
          proofBundle: bundle,
          nowMs: now,
        };
      },
    },
  ];

  for (const scenario of scenarios) {
    const check = recomputeReference(scenario.build(), 12_345_678);
    assert.equal(check.consistent, true, scenario.name);
    assert.notEqual(check.first.computedAtMs, check.second.computedAtMs);
    assert.deepEqual(
      check.first.cookies.map((entry) => [entry.send, entry.reason]),
      check.second.cookies.map((entry) => [entry.send, entry.reason]),
      scenario.name,
    );
  }
});

import { describe, it } from 'node:test';
import assert from 'node:assert/strict';

import {
  ClientHintManager,
  LOW_ENTROPY_HINTS,
  canonicalHeaderName,
  hintToFeature,
  parseAcceptCh,
  parsePermissionsPolicy,
  type HeaderBag,
  type RequestInfo,
} from '../src/client-hints.ts';

const SITE = 'https://example.com';
const THIRD_PARTY_A = 'https://analytics.example.net';
const THIRD_PARTY_B = 'https://ads.example.org';

const ALL_HINT_VALUES: HeaderBag = {
  'Sec-CH-UA': '"Chromium";v="120"',
  'Sec-CH-UA-Mobile': '?0',
  'Sec-CH-UA-Platform': '"macOS"',
  'Sec-CH-UA-Full-Version-List': '"Chromium";v="120.0.6099.71"',
  'Sec-CH-UA-Model': '"MacBookPro"',
};

const ACCEPT_CH =
  'Sec-CH-UA-Full-Version-List, Sec-CH-UA-Model, Sec-CH-UA-Arch';

function managerWithTopLevelAcceptCh(
  permissionsPolicy?: string | readonly string[],
  acceptCh: string = ACCEPT_CH,
): ClientHintManager {
  const manager = new ClientHintManager({
    hintValues: ALL_HINT_VALUES,
    permissionsPolicy,
  });
  manager.observeResponse({
    url: `${SITE}/home`,
    headers: { 'Accept-CH': acceptCh },
    isTopLevelNavigation: true,
  });
  return manager;
}

function requestTo(url: string): RequestInfo {
  return { url, clientOrigin: SITE, headers: {} };
}

function headerNames(headers: HeaderBag): string[] {
  return Object.keys(headers).sort();
}

describe('ClientHintManager 附加规则', () => {
  it('同源请求：默认发低熵提示，高熵提示需 Accept-CH 声明', () => {
    const manager = new ClientHintManager({ hintValues: ALL_HINT_VALUES });

    const beforeAcceptCh = manager.attachClientHints(requestTo(`${SITE}/api`));
    assert.deepEqual(headerNames(beforeAcceptCh), [
      'Sec-CH-UA',
      'Sec-CH-UA-Mobile',
      'Sec-CH-UA-Platform',
    ]);

    manager.observeResponse({
      url: `${SITE}/home`,
      headers: { 'Accept-CH': ACCEPT_CH },
      isTopLevelNavigation: true,
    });

    const afterAcceptCh = manager.attachClientHints(requestTo(`${SITE}/api`));
    assert.deepEqual(headerNames(afterAcceptCh), [
      'Sec-CH-UA',
      'Sec-CH-UA-Full-Version-List',
      'Sec-CH-UA-Mobile',
      'Sec-CH-UA-Model',
      'Sec-CH-UA-Platform',
    ]);
    assert.equal(
      afterAcceptCh['Sec-CH-UA-Full-Version-List'],
      '"Chromium";v="120.0.6099.71"',
    );
  });

  it('场景一：无委托的跨源请求不发送任何提示（含低熵）', () => {
    const manager = managerWithTopLevelAcceptCh();

    const headers = manager.attachClientHints(requestTo(`${THIRD_PARTY_A}/track`));
    assert.deepEqual(headerNames(headers), []);
  });

  it('场景二：允许列表显式包含目标来源时，仅该跨源来源收到被声明的提示', () => {
    const policy =
      `ch-ua-full-version-list=("${THIRD_PARTY_A}"), ` +
      `ch-ua-model=("${THIRD_PARTY_A}"), ` +
      `ch-ua=("${THIRD_PARTY_A}"), ` +
      `ch-ua-mobile=("${THIRD_PARTY_A}"), ` +
      `ch-ua-platform=("${THIRD_PARTY_A}")`;
    const manager = managerWithTopLevelAcceptCh(policy);

    const delegated = manager.attachClientHints(
      requestTo(`${THIRD_PARTY_A}/collect`),
    );
    assert.deepEqual(headerNames(delegated), [
      'Sec-CH-UA',
      'Sec-CH-UA-Full-Version-List',
      'Sec-CH-UA-Mobile',
      'Sec-CH-UA-Model',
      'Sec-CH-UA-Platform',
    ]);

    // 另一个第三方来源不在任何列表中，仍然收不到提示。
    const other = manager.attachClientHints(requestTo(`${THIRD_PARTY_B}/px`));
    assert.deepEqual(headerNames(other), []);
  });

  it('场景三：明文页面（HTTP）里的 Accept-CH 声明被忽略，且 HTTP 请求不发提示', () => {
    const manager = new ClientHintManager({ hintValues: ALL_HINT_VALUES });

    manager.observeResponse({
      url: 'http://example.com/home',
      headers: { 'Accept-CH': ACCEPT_CH },
      isTopLevelNavigation: true,
    });
    assert.deepEqual(manager.getAcceptedHints(SITE), []);

    // 即使明文来源记录过声明，明文请求本身也不携带任何 Client Hint。
    const plaintextRequest = manager.attachClientHints({
      url: 'http://example.com/api',
      clientOrigin: 'http://example.com',
      headers: {},
    });
    assert.deepEqual(headerNames(plaintextRequest), []);

    // 同一台主机的 HTTPS 页面没有声明，高熵提示不会泄露。
    const httpsRequest = manager.attachClientHints(requestTo(`${SITE}/api`));
    assert.deepEqual(headerNames(httpsRequest), [
      'Sec-CH-UA',
      'Sec-CH-UA-Mobile',
      'Sec-CH-UA-Platform',
    ]);
  });

  it('场景四：子资源响应里的 Accept-CH 声明被忽略', () => {
    const manager = new ClientHintManager({ hintValues: ALL_HINT_VALUES });

    manager.observeResponse({
      url: `${SITE}/app.js`,
      headers: { 'Accept-CH': ACCEPT_CH },
      isTopLevelNavigation: false,
    });
    assert.deepEqual(manager.getAcceptedHints(SITE), []);

    const headers = manager.attachClientHints(requestTo(`${SITE}/api`));
    assert.deepEqual(headerNames(headers), [
      'Sec-CH-UA',
      'Sec-CH-UA-Mobile',
      'Sec-CH-UA-Platform',
    ]);
  });

  it('场景五：允许列表为 * 时放行任意跨源来源', () => {
    const manager = managerWithTopLevelAcceptCh(
      'ch-ua-full-version-list=*, ch-ua-model=*, ch-ua=*, ch-ua-mobile=*, ch-ua-platform=*',
    );

    const first = manager.attachClientHints(requestTo(`${THIRD_PARTY_A}/a`));
    const second = manager.attachClientHints(requestTo(`${THIRD_PARTY_B}/b`));
    const expected = [
      'Sec-CH-UA',
      'Sec-CH-UA-Full-Version-List',
      'Sec-CH-UA-Mobile',
      'Sec-CH-UA-Model',
      'Sec-CH-UA-Platform',
    ];
    assert.deepEqual(headerNames(first), expected);
    assert.deepEqual(headerNames(second), expected);
  });

  it('空允许列表 () 完全禁止该特性，即使同源也不发送', () => {
    const manager = managerWithTopLevelAcceptCh('ch-ua-model=()');

    const headers = manager.attachClientHints(requestTo(`${SITE}/api`));
    assert.ok(!('Sec-CH-UA-Model' in headers));
    assert.ok('Sec-CH-UA-Full-Version-List' in headers);
  });

  it('self 与显式来源混合，且只委托高熵提示时低熵不跨源', () => {
    const manager = managerWithTopLevelAcceptCh(
      `ch-ua-model=(self "${THIRD_PARTY_A}")`,
    );

    const sameOrigin = manager.attachClientHints(requestTo(`${SITE}/api`));
    assert.ok('Sec-CH-UA-Model' in sameOrigin);

    const crossOrigin = manager.attachClientHints(
      requestTo(`${THIRD_PARTY_A}/collect`),
    );
    assert.deepEqual(headerNames(crossOrigin), ['Sec-CH-UA-Model']);
  });

  it('未被 Accept-CH 声明的高熵提示即使被委托也不发送', () => {
    const manager = new ClientHintManager({
      hintValues: ALL_HINT_VALUES,
      permissionsPolicy: 'ch-ua-arch=*',
    });
    // 页面没有任何有效 Accept-CH。
    const headers = manager.attachClientHints(requestTo(`${THIRD_PARTY_A}/x`));
    assert.deepEqual(headerNames(headers), []);
  });

  it('多个 Permissions-Policy 头取并集，且按来源而非路径匹配', () => {
    const manager = managerWithTopLevelAcceptCh([
      `ch-ua-model=("${THIRD_PARTY_A}")`,
      `ch-ua-full-version-list=("${THIRD_PARTY_A}")`,
    ]);

    const headers = manager.attachClientHints(
      requestTo(`${THIRD_PARTY_A}/any/path?x=1`),
    );
    assert.ok('Sec-CH-UA-Model' in headers);
    assert.ok('Sec-CH-UA-Full-Version-List' in headers);
    assert.ok(!('Sec-CH-UA' in headers));
  });

  it('Accept-CH 按顶层来源分别记录，导航到新来源后旧声明不串用', () => {
    const manager = managerWithTopLevelAcceptCh();

    manager.observeResponse({
      url: `${THIRD_PARTY_B}/welcome`,
      headers: { 'Accept-CH': 'Sec-CH-UA-Model' },
      isTopLevelNavigation: true,
    });

    assert.deepEqual(manager.getAcceptedHints(SITE), [
      'sec-ch-ua-full-version-list',
      'sec-ch-ua-model',
      'sec-ch-ua-arch',
    ]);
    assert.deepEqual(manager.getAcceptedHints(THIRD_PARTY_B), [
      'sec-ch-ua-model',
    ]);

    const fromOtherPage = manager.attachClientHints({
      url: `${THIRD_PARTY_B}/api`,
      clientOrigin: THIRD_PARTY_B,
      headers: {},
    });
    assert.ok('Sec-CH-UA-Model' in fromOtherPage);
    assert.ok(!('Sec-CH-UA-Full-Version-List' in fromOtherPage));
  });

  it('同名头以大小写不敏感方式覆盖，不产生重复键', () => {
    const manager = managerWithTopLevelAcceptCh();
    const request = requestTo(`${SITE}/api`);
    request.headers = { 'sec-ch-ua-model': 'stale' };

    const headers = manager.attachClientHints(request);
    assert.equal(headers['Sec-CH-UA-Model'], '"MacBookPro"');
    assert.equal(Object.keys(headers).length, 5);
  });
});

describe('解析工具', () => {
  it('parseAcceptCh 去重、转小写、忽略空条目', () => {
    assert.deepEqual(
      parseAcceptCh(' Sec-CH-UA-Model, sec-ch-ua-model, , Sec-CH-UA '),
      ['sec-ch-ua-model', 'sec-ch-ua'],
    );
  });

  it('parsePermissionsPolicy 处理 *、self、引号来源与畸形指令', () => {
    const policy = parsePermissionsPolicy(
      `ch-ua-model=*, ch-ua= (self "${THIRD_PARTY_A}"), ` +
        'broken-directive, ch-ua-arch=("not a url")',
    );

    assert.deepEqual(policy.get('ch-ua-model'), {
      wildcard: true,
      self: false,
      origins: [],
    });
    assert.deepEqual(policy.get('ch-ua'), {
      wildcard: false,
      self: true,
      origins: [THIRD_PARTY_A],
    });
    assert.equal(policy.has('broken-directive'), false);
    assert.deepEqual(policy.get('ch-ua-arch'), {
      wildcard: false,
      self: false,
      origins: [],
    });
  });

  it('特性名映射与标准头名', () => {
    assert.equal(
      hintToFeature('Sec-CH-UA-Full-Version-List'),
      'ch-ua-full-version-list',
    );
    assert.equal(canonicalHeaderName('sec-ch-ua-model'), 'Sec-CH-UA-Model');
    assert.equal(LOW_ENTROPY_HINTS.size, 3);
  });
});

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { RdapAdmission } from '../src/admission.ts';
import { BootstrapRegistry } from '../src/bootstrap.ts';
import { NegativeCache } from '../src/cache.ts';
import {
  BOOTSTRAP_DOC,
  domainBody,
  makeFetch,
  makeRegistry,
  ok,
  PUBLIC_RESOLVER,
  redirect,
} from './helpers.ts';

const DE_URL = 'https://rdap.denic.de/domain/example.de';

function makeAdmission(
  routes: Parameters<typeof makeFetch>[0],
  options: Record<string, unknown> = {},
) {
  const mock = makeFetch(routes);
  const admission = new RdapAdmission({
    bootstrap: makeRegistry(),
    fetch: mock.fetch,
    resolver: PUBLIC_RESOLVER,
    ...options,
  });
  return { admission, mock };
}

test('内网链接: 指向私网/非https/rel不符/指外self的链接整条丢弃', async () => {
  const body = domainBody('example.de', {
    links: [
      { rel: 'self', href: 'https://rdap.denic.de/domain/example.de' },
      { rel: 'related', href: 'https://10.0.0.8/internal' },
      { rel: 'related', href: 'https://169.254.169.254/latest/meta-data' },
      { rel: 'related', href: 'https://[fd00::1]/internal' },
      { rel: 'related', href: 'http://rdap.denic.de/insecure' },
      { rel: 'bogus', href: 'https://rdap.denic.de/domain/example.de' },
      { rel: 'self', href: 'https://evil.example.com/domain/example.de' },
    ],
  });
  const { admission } = makeAdmission({ [DE_URL]: ok(body) });
  const result = await admission.query('example.de');
  assert.equal(result.ok, true);
  if (!result.ok) return;
  const links = result.response.links as Array<Record<string, unknown>>;
  assert.equal(links.length, 1);
  assert.equal(links[0].rel, 'self');
  assert.equal(links[0].href, 'https://rdap.denic.de/domain/example.de');
});

test('内网链接: 重定向到私网地址立即终止且不发请求', async () => {
  const target = 'https://169.254.169.254/latest/meta-data';
  const { admission, mock } = makeAdmission({
    [DE_URL]: redirect(target),
    [target]: ok(domainBody('example.de')),
  });
  const result = await admission.query('example.de');
  assert.equal(result.ok, false);
  if (result.ok) return;
  assert.equal(result.reason, 'private-address');
  assert.deepEqual(mock.calls, [DE_URL]);
});

test('内网链接: DNS解析到私网地址同样被拒绝', async () => {
  const { admission } = makeAdmission(
    { [DE_URL]: ok(domainBody('example.de')) },
    { resolver: async () => ['192.168.1.1'] },
  );
  const result = await admission.query('example.de');
  assert.equal(result.ok, false);
  if (result.ok) return;
  assert.equal(result.reason, 'private-address');
});

test('重定向环: A->B->A 检测为环并终止', async () => {
  const a = 'https://rdap.denic.de/domain/example.de';
  const b = 'https://rdap.denic.de/redirect/b';
  const { admission } = makeAdmission({
    [a]: redirect(b),
    [b]: redirect(a),
  });
  const result = await admission.query('example.de');
  assert.equal(result.ok, false);
  if (result.ok) return;
  assert.equal(result.reason, 'redirect-loop');
});

test('重定向环: 超过三次重定向被拒绝', async () => {
  const hops = [
    'https://rdap.denic.de/domain/example.de',
    'https://rdap.denic.de/r1',
    'https://rdap.denic.de/r2',
    'https://rdap.denic.de/r3',
    'https://rdap.denic.de/r4',
  ];
  const routes: Record<string, ReturnType<typeof ok>> = {};
  for (let i = 0; i < hops.length - 1; i++) {
    routes[hops[i]] = redirect(hops[i + 1]);
  }
  routes[hops[hops.length - 1]] = ok(domainBody('example.de'));
  const { admission } = makeAdmission(routes);
  const result = await admission.query('example.de');
  assert.equal(result.ok, false);
  if (result.ok) return;
  assert.equal(result.reason, 'too-many-redirects');
});

test('重定向: 跨出允许主机立即终止', async () => {
  const evil = 'https://rdap.attacker.example/domain/example.de';
  const { admission, mock } = makeAdmission({
    [DE_URL]: redirect(evil),
    [evil]: ok(domainBody('example.de')),
  });
  const result = await admission.query('example.de');
  assert.equal(result.ok, false);
  if (result.ok) return;
  assert.equal(result.reason, 'host-not-allowed');
  assert.deepEqual(mock.calls, [DE_URL]);
});

test('重定向: 调用方白名单内的主机允许跟随', async () => {
  const alt = 'https://rdap.partner.example/domain/example.de';
  const { admission } = makeAdmission(
    {
      [DE_URL]: redirect(alt),
      [alt]: ok(domainBody('example.de')),
    },
    { whitelist: ['rdap.partner.example'] },
  );
  const result = await admission.query('example.de');
  assert.equal(result.ok, true);
  if (!result.ok) return;
  assert.deepEqual(result.chain, [DE_URL, alt]);
});

test('引导: TLD不在IANA引导文件中时无可用服务器', async () => {
  const { admission } = makeAdmission({});
  const result = await admission.query('example.xyz');
  assert.equal(result.ok, false);
  if (result.ok) return;
  assert.equal(result.reason, 'no-bootstrap-server');
});

test('IDN混合大小写: 查询名与ldhName按小写Punycode逐标签匹配', async () => {
  const url = 'https://rdap.denic.de/domain/xn--bcher-kva.de';
  const { admission, mock } = makeAdmission({
    [url]: ok(domainBody('XN--BCHER-KVA.DE')),
  });
  const result = await admission.query('BüCHER.de');
  assert.equal(result.ok, true);
  if (!result.ok) return;
  assert.equal(result.name, 'xn--bcher-kva.de');
  assert.deepEqual(mock.calls, [url]);
});

test('IDN混合大小写: ldhName与查询对象不一致被拒绝', async () => {
  const url = 'https://rdap.denic.de/domain/xn--bcher-kva.de';
  const { admission } = makeAdmission({
    [url]: ok(domainBody('xn--bcher-kvb.de')),
  });
  const result = await admission.query('bücher.de');
  assert.equal(result.ok, false);
  if (result.ok) return;
  assert.equal(result.reason, 'name-mismatch');
});

test('错类型对象: objectClassName不符被拒绝', async () => {
  const body = JSON.stringify({
    objectClassName: 'entity',
    rdapConformance: ['rdap_level_0'],
    ldhName: 'example.de',
  });
  const { admission } = makeAdmission({ [DE_URL]: ok(body) });
  const result = await admission.query('example.de');
  assert.equal(result.ok, false);
  if (result.ok) return;
  assert.equal(result.reason, 'unexpected-object-class');
});

test('错类型对象: rdapConformance缺失被拒绝', async () => {
  const body = JSON.stringify({
    objectClassName: 'domain',
    ldhName: 'example.de',
  });
  const { admission } = makeAdmission({ [DE_URL]: ok(body) });
  const result = await admission.query('example.de');
  assert.equal(result.ok, false);
  if (result.ok) return;
  assert.equal(result.reason, 'missing-conformance');
});

test('负缓存: 校验失败不写入正缓存,按查询名与响应体哈希记入负缓存', async () => {
  const badBody = domainBody('other.de');
  const { admission } = makeAdmission({ [DE_URL]: ok(badBody) });
  const first = await admission.query('example.de');
  assert.equal(first.ok, false);
  assert.equal(admission.positiveCache.get('example.de'), undefined);
  const hash = createHash('sha256').update(badBody).digest('hex');
  const record = admission.negativeCache.get('example.de', hash);
  assert.ok(record);
  assert.equal(record.reason, 'name-mismatch');

  const second = await admission.query('example.de');
  assert.equal(second.ok, false);
  if (second.ok) return;
  assert.equal(second.fromCache, true);
  assert.equal(second.reason, 'name-mismatch');
});

test('负缓存: 不同响应体不受负缓存阻断且独立校验', async () => {
  const badBody = domainBody('other.de');
  const goodBody = domainBody('example.de');
  let body = badBody;
  const { admission } = makeAdmission({
    [DE_URL]: () => ok(body),
  });
  const first = await admission.query('example.de');
  assert.equal(first.ok, false);
  body = goodBody;
  const second = await admission.query('example.de');
  assert.equal(second.ok, true);
  if (!second.ok) return;
  assert.equal(second.fromCache, false);
  assert.ok(admission.positiveCache.get('example.de'));
});

test('负缓存: TTL到期后条目失效', async () => {
  let now = 1_000_000;
  const badBody = domainBody('other.de');
  const { admission } = makeAdmission(
    { [DE_URL]: ok(badBody) },
    { now: () => now, negativeTtlMs: 5000 },
  );
  const first = await admission.query('example.de');
  assert.equal(first.ok, false);
  const hash = createHash('sha256').update(badBody).digest('hex');
  assert.ok(admission.negativeCache.get('example.de', hash));
  now += 6000;
  assert.equal(admission.negativeCache.get('example.de', hash), undefined);
  const second = await admission.query('example.de');
  assert.equal(second.ok, false);
  if (second.ok) return;
  assert.equal(second.fromCache, false);
});

test('负缓存: 正缓存命中不触发网络请求', async () => {
  const { admission, mock } = makeAdmission({
    [DE_URL]: ok(domainBody('example.de')),
  });
  const first = await admission.query('example.de');
  assert.equal(first.ok, true);
  const second = await admission.query('EXAMPLE.DE');
  assert.equal(second.ok, true);
  if (!second.ok) return;
  assert.equal(second.fromCache, true);
  assert.equal(mock.calls.length, 1);
});

test('引导: 非https的引导URL被注册表忽略', () => {
  const registry = makeRegistry();
  assert.ok(registry.isBootstrapHost('rdap.denic.de'));
  assert.ok(registry.isBootstrapHost('RDAP.DENIC.DE'));
  assert.ok(!registry.isBootstrapHost('evil.example.com'));
  const doc = {
    services: [[['xyz'], ['http://insecure.example/']]],
  };
  const reg2 = BootstrapRegistry.fromIANA(doc);
  assert.equal(reg2.baseUrlsForName('a.xyz').length, 0);
});

test('引导: 使用当前引导文件内容选择服务器', async () => {
  assert.ok(BOOTSTRAP_DOC.services.length > 0);
  const url = 'https://rdap.verisign.com/domain/example.com';
  const { admission } = makeAdmission({ [url]: ok(domainBody('example.com')) });
  const result = await admission.query('EXAMPLE.com');
  assert.equal(result.ok, true);
});

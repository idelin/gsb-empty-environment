import { test } from "node:test";
import assert from "node:assert/strict";
import {
  AdmissionCache,
  BootstrapRegistry,
  RdapAdmissionError,
  RdapClient,
} from "../index.ts";
import {
  bootstrapFixture,
  COM_BASE,
  domainResponse,
  mockFetch,
} from "./fixtures.ts";

function registryLoaded() {
  const registry = new BootstrapRegistry();
  registry.load(bootstrapFixture);
  return registry;
}

const COM_QUERY_PREFIX = `${COM_BASE}domain/`;

test("正常查询：引导来源 + 一致实体被准入并写入缓存，二次命中缓存", async () => {
  const fetchImpl = mockFetch([
    [COM_QUERY_PREFIX, { body: domainResponse("example.com") }],
  ]);
  const cache = new AdmissionCache();
  const client = new RdapClient({ registry: registryLoaded(), fetchImpl, cache });

  const first = await client.query("example.com");
  assert.equal(first.fromCache, false);
  assert.equal(first.body.ldhName, "example.com");
  assert.equal(first.redirects, 0);
  assert.equal(cache.size, 1);

  const second = await client.query("EXAMPLE.COM");
  assert.equal(second.fromCache, true);
});

test("IDN 大小写：Unicode/大写 Punycode/小写 Punycode 视为同一查询并命中缓存", async () => {
  const fetchImpl = mockFetch([
    [COM_QUERY_PREFIX, { body: domainResponse("xn--bcher-kva.com") }],
  ]);
  const cache = new AdmissionCache();
  const client = new RdapClient({ registry: registryLoaded(), fetchImpl, cache });

  const viaUnicode = await client.query("Bücher.com");
  assert.equal(viaUnicode.fromCache, false);
  assert.equal(viaUnicode.body.ldhName, "xn--bcher-kva.com");
  assert.equal(cache.size, 1);

  const viaUpperPuny = await client.query("XN--BCHER-KVA.COM");
  assert.equal(viaUpperPuny.fromCache, true);

  const viaLowerPuny = await client.query("xn--bcher-kva.com");
  assert.equal(viaLowerPuny.fromCache, true);
});

test("内网链接：重定向到 192.168/127 等私有地址被拒绝且不缓存", async () => {
  const intranetTargets = [
    "https://192.168.1.10/metadata",
    "https://127.0.0.1:8443/admin",
    "http://rdap.verisign.com/plain-http",
    "https://attacker.test/exfil",
  ];
  for (const location of intranetTargets) {
    const fetchImpl = mockFetch([
      [COM_QUERY_PREFIX, {
        status: 302,
        headers: { location },
      }],
    ]);
    const cache = new AdmissionCache();
    const client = new RdapClient({ registry: registryLoaded(), fetchImpl, cache });
    await assert.rejects(
      () => client.query("example.com"),
      (error: unknown) => {
        assert.ok(error instanceof RdapAdmissionError);
        assert.ok(
          ["redirect-private-address", "redirect-not-https", "redirect-host-not-allowed"]
            .includes(error.failure),
        );
        return true;
      },
    );
    assert.equal(cache.size, 0, `恶意落点 ${location} 不得写入缓存`);
  }
});

test("应答 links 中的内网链接被 safeLinks 剔除，绝不进入可跟随集合", async () => {
  const body = domainResponse("example.com", {
    links: [
      { rel: "self", href: `${COM_BASE}domain/example.com` },
      { rel: "evil", href: "https://10.0.0.2/internal" },
      { rel: "evil", href: "https://169.254.169.254/latest/meta-data" },
    ],
  });
  const fetchImpl = mockFetch([[COM_QUERY_PREFIX, { body }]]);
  const client = new RdapClient({ registry: registryLoaded(), fetchImpl });
  const outcome = await client.query("example.com");
  const safe = client.safeLinks(outcome.body, COM_BASE);
  assert.equal(safe.length, 1);
  assert.equal(safe[0].rel, "self");
});

test("错类型对象：返回 nameserver/entity 整条丢弃且不入缓存", async () => {
  for (const objectClassName of ["nameserver", "entity", "ip network"]) {
    const fetchImpl = mockFetch([
      [COM_QUERY_PREFIX, {
        body: domainResponse("example.com", { objectClassName }),
      }],
    ]);
    const cache = new AdmissionCache();
    const client = new RdapClient({ registry: registryLoaded(), fetchImpl, cache });
    await assert.rejects(
      () => client.query("example.com"),
      (error: unknown) =>
        error instanceof RdapAdmissionError && error.failure === "admission-rejected",
    );
    assert.equal(cache.size, 0);
  }
});

test("ldhName 不一致：返回其他域名对象同样整条丢弃", async () => {
  const fetchImpl = mockFetch([
    [COM_QUERY_PREFIX, { body: domainResponse("attacker.com") }],
  ]);
  const cache = new AdmissionCache();
  const client = new RdapClient({ registry: registryLoaded(), fetchImpl, cache });
  const error = await client.query("victim.com").catch((e) => e as RdapAdmissionError);
  assert.ok(error instanceof RdapAdmissionError);
  assert.equal(error.failure, "admission-rejected");
  assert.match(error.message, /ldh-name-mismatch/);
  assert.equal(cache.size, 0);
});

test("引导缺项：TLD 不在 IANA 引导文件中时拒绝查询", async () => {
  const fetchImpl = mockFetch([]);
  const client = new RdapClient({ registry: registryLoaded(), fetchImpl });
  const error = await client.query("brand.xyz").catch((e) => e as RdapAdmissionError);
  assert.ok(error instanceof RdapAdmissionError);
  assert.equal(error.failure, "bootstrap-missing");
});

test("重定向最多三次：第 4 跳触发 redirect-limit-exceeded", async () => {
  const allowedHost = "rdap.verisign.com";
  const hop = (n: number) => `https://${allowedHost}/hop${n}`;
  const fetchImpl = mockFetch([
    [COM_QUERY_PREFIX, { status: 302, headers: { location: hop(1) } }],
    [hop(1), { status: 302, headers: { location: hop(2) } }],
    [hop(2), { status: 302, headers: { location: hop(3) } }],
    [hop(3), { status: 302, headers: { location: hop(4) } }],
    [hop(4), { body: domainResponse("example.com") }],
  ]);
  const client = new RdapClient({ registry: registryLoaded(), fetchImpl });
  const error = await client.query("example.com").catch((e) => e as RdapAdmissionError);
  assert.ok(error instanceof RdapAdmissionError);
  assert.equal(error.failure, "redirect-limit-exceeded");
});

test("三次以内的合法 https 重定向（白名单主机）可正常完成", async () => {
  const hop = (n: number) => `https://rdap.publicinterestregistry.org/hop${n}`;
  const fetchImpl = mockFetch([
    [COM_QUERY_PREFIX, { status: 302, headers: { location: hop(1) } }],
    [hop(1), { status: 302, headers: { location: hop(2) } }],
    [hop(2), { status: 302, headers: { location: hop(3) } }],
    [hop(3), { body: domainResponse("example.com") }],
  ]);
  // publicinterestregistry 是 .org 引导主机，已在引导主机并集中，无需白名单。
  const client = new RdapClient({ registry: registryLoaded(), fetchImpl });
  const outcome = await client.query("example.com");
  assert.equal(outcome.redirects, 3);
  assert.equal(outcome.resolvedHost, "rdap.publicinterestregistry.org");
});

test("调用方白名单主机允许跟随，但 http 与私有地址即使在白名单也拒绝", async () => {
  const whitelisted = "https://partner.rdap.example/data";
  const fetchImpl = mockFetch([
    [COM_QUERY_PREFIX, { status: 302, headers: { location: whitelisted } }],
    [whitelisted, { body: domainResponse("example.com") }],
  ]);
  const ok = new RdapClient({
    registry: registryLoaded(),
    fetchImpl,
    hostWhitelist: ["partner.rdap.example"],
  });
  const outcome = await ok.query("example.com");
  assert.equal(outcome.resolvedHost, "partner.rdap.example");

  const denied = new RdapClient({
    registry: registryLoaded(),
    fetchImpl: mockFetch([
      [COM_QUERY_PREFIX, {
        status: 302,
        headers: { location: "https://10.1.2.3/x" },
      }],
    ]),
    hostWhitelist: ["10.1.2.3"],
  });
  await assert.rejects(
    () => denied.query("example.com"),
    (error: unknown) =>
      error instanceof RdapAdmissionError && error.failure === "redirect-private-address",
  );
});

test("非法查询名与未加载引导被显式拒绝", async () => {
  const client = new RdapClient({ registry: new BootstrapRegistry(), fetchImpl: mockFetch([]) });
  const badName = await client.query("not a domain").catch((e) => e as RdapAdmissionError);
  assert.equal(badName.failure, "invalid-query-name");

  const notLoaded = await client.query("example.com").catch((e) => e as RdapAdmissionError);
  assert.equal(notLoaded.failure, "bootstrap-not-loaded");
});

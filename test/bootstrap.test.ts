import { test } from "node:test";
import assert from "node:assert/strict";
import {
  BootstrapRegistry,
  parseBootstrap,
  tldOf,
} from "../index.ts";
import { bootstrapFixture, COM_BASE } from "./fixtures.ts";

test("tldOf 取最右标签并大写", () => {
  assert.equal(tldOf("foo.bar.com"), "COM");
  assert.equal(tldOf("example.org."), "ORG");
});

test("parseBootstrap 提取 https 基址与主机集合", () => {
  const entries = parseBootstrap(bootstrapFixture);
  assert.equal(entries.length, 3);
  assert.deepEqual(entries[0].tlds, ["COM", "NET"]);
  assert.equal(entries[0].baseUrls[0].toString(), COM_BASE);
  assert.ok(entries[0].hosts.has("rdap.verisign.com"));
});

test("parseBootstrap 拒绝非 https 基址与畸形服务项", () => {
  assert.throws(() =>
    parseBootstrap({
      services: [[["com"], ["http://insecure.example/"]]],
    })
  );
  assert.throws(() => parseBootstrap({ services: [["com"]] }));
  assert.throws(() => parseBootstrap({} as unknown as { services: unknown }));

  // 结构合法但为空：得到空注册表（后续 match 一律按缺项处理）。
  assert.deepEqual(parseBootstrap({ services: [] }), []);
});

test("BootstrapRegistry 命中 TLD 并暴露主机并集", () => {
  const registry = new BootstrapRegistry();
  registry.load(bootstrapFixture);
  assert.equal(registry.isLoaded, true);
  assert.equal(registry.match("shop.example.com")?.baseUrl.toString(), COM_BASE);

  const hosts = registry.allBootstrapHosts();
  assert.ok(hosts.has("rdap.verisign.com"));
  assert.ok(hosts.has("rdap.jprs.jp"));
});

test("BootstrapRegistry 对引导缺项返回 null（不得退化到任意服务器）", () => {
  const registry = new BootstrapRegistry();
  registry.load(bootstrapFixture);
  assert.equal(registry.match("something.xyz"), null);
  assert.equal(registry.match("no-such-tld.zzz"), null);
});

test("未加载的注册表在 match 时显式报错", () => {
  const registry = new BootstrapRegistry();
  assert.throws(() => registry.match("example.com"));
});

import { test } from "node:test";
import assert from "node:assert/strict";
import { loadWithPlan } from "../src/loader.ts";
import { MockLoader } from "../src/mock-loader.ts";
import type { DocumentPolicy } from "../src/types.ts";

const secureDefault: DocumentPolicy = {
  secureContext: true,
  upgradeInsecureRequests: false,
  blockAllMixedContent: false,
};

test("升级成功后只请求 https，不产生明文尝试", async () => {
  const mock = new MockLoader().respond("https://cdn.example.com/logo.png", {
    status: 200,
    body: "png",
  });
  const result = await loadWithPlan({
    url: "http://cdn.example.com/logo.png",
    resourceType: "image",
    policy: secureDefault,
    loader: mock.asLoader(),
  });
  assert.equal(result.ok, true);
  assert.deepEqual(mock.attempts, ["https://cdn.example.com/logo.png"]);
});

test("升级请求失败时不回落明文，重试仍使用升级后的 URL", async () => {
  const mock = new MockLoader()
    .fail("https://cdn.example.com/logo.png", "connection reset")
    .respond("https://cdn.example.com/logo.png", { status: 200, body: "png" });

  const first = await loadWithPlan({
    url: "http://cdn.example.com/logo.png",
    resourceType: "image",
    policy: secureDefault,
    loader: mock.asLoader(),
  });
  assert.equal(first.ok, false);
  assert.match(first.error!, /load-failed/);

  // 重试：重新走完整规划，仍只尝试 https。
  const retry = await loadWithPlan({
    url: "http://cdn.example.com/logo.png",
    resourceType: "image",
    policy: secureDefault,
    loader: mock.asLoader(),
  });
  assert.equal(retry.ok, true);
  assert.deepEqual(mock.attempts, [
    "https://cdn.example.com/logo.png",
    "https://cdn.example.com/logo.png",
  ]);
});

test("阻断时不发起任何网络请求", async () => {
  const mock = new MockLoader();
  const result = await loadWithPlan({
    url: "http://cdn.example.com/app.js",
    resourceType: "script",
    policy: secureDefault,
    loader: mock.asLoader(),
  });
  assert.equal(result.ok, false);
  assert.match(result.error!, /blocked/);
  assert.deepEqual(mock.attempts, []);
});

test("重定向链上每一跳都重新检查升级要求", async () => {
  const mock = new MockLoader()
    .respond("https://cdn.example.com/logo.png", {
      status: 302,
      redirectTo: "http://static.example.com/logo.png",
    })
    .respond("https://static.example.com/logo.png", { status: 200, body: "png" });

  const result = await loadWithPlan({
    url: "http://cdn.example.com/logo.png",
    resourceType: "image",
    policy: secureDefault,
    loader: mock.asLoader(),
  });
  assert.equal(result.ok, true);
  // 重定向到明文 URL 后再次被升级，绝不直接请求 http。
  assert.deepEqual(mock.attempts, [
    "https://cdn.example.com/logo.png",
    "https://static.example.com/logo.png",
  ]);
  assert.deepEqual(
    result.plans.map((p) => p.action),
    ["upgrade", "upgrade"],
  );
});

test("重定向目标沿升级要求继续检查，升级后失败不回落", async () => {
  const mock = new MockLoader().respond("https://cdn.example.com/app.js", {
    status: 302,
    redirectTo: "http://evil.example.com/app.js",
  });
  const result = await loadWithPlan({
    url: "http://cdn.example.com/app.js",
    resourceType: "script",
    policy: { ...secureDefault, upgradeInsecureRequests: true },
    loader: mock.asLoader(),
  });
  // upgradeInsecureRequests 下重定向目标同样升级，而不是阻断或放行明文。
  assert.equal(result.ok, false);
  assert.deepEqual(mock.attempts, [
    "https://cdn.example.com/app.js",
    "https://evil.example.com/app.js",
  ]);
});

test("相对重定向基于当前（升级后）URL 解析", async () => {
  const mock = new MockLoader()
    .respond("https://cdn.example.com/a/logo.png", {
      status: 302,
      redirectTo: "../b/logo.png",
    })
    .respond("https://cdn.example.com/b/logo.png", { status: 200 });
  const result = await loadWithPlan({
    url: "http://cdn.example.com/a/logo.png",
    resourceType: "image",
    policy: secureDefault,
    loader: mock.asLoader(),
  });
  assert.equal(result.ok, true);
  assert.deepEqual(mock.attempts, [
    "https://cdn.example.com/a/logo.png",
    "https://cdn.example.com/b/logo.png",
  ]);
});

test("内网地址走同一套升级规则，失败同样不回落", async () => {
  const mock = new MockLoader().fail("https://192.168.1.10/logo.png", "unreachable");
  const result = await loadWithPlan({
    url: "http://192.168.1.10/logo.png",
    resourceType: "image",
    policy: secureDefault,
    loader: mock.asLoader(),
  });
  assert.equal(result.ok, false);
  assert.deepEqual(mock.attempts, ["https://192.168.1.10/logo.png"]);
});

test("缓存命中不改变安全要求：先升级再查缓存", async () => {
  const mock = new MockLoader().respond("https://cdn.example.com/logo.png", {
    status: 200,
    fromCache: true,
    body: "cached-png",
  });
  const result = await loadWithPlan({
    url: "http://cdn.example.com/logo.png",
    resourceType: "image",
    policy: secureDefault,
    loader: mock.asLoader(),
  });
  assert.equal(result.ok, true);
  assert.equal(result.response?.fromCache, true);
  // 缓存键也是升级后的安全 URL，从未出现明文尝试。
  assert.deepEqual(mock.attempts, ["https://cdn.example.com/logo.png"]);
  assert.equal(result.plans[0].action, "upgrade");
});

test("超过最大重定向次数时报错", async () => {
  const mock = new MockLoader().respond("https://cdn.example.com/logo.png", {
    status: 302,
    redirectTo: "https://cdn.example.com/logo.png",
  });
  const result = await loadWithPlan({
    url: "http://cdn.example.com/logo.png",
    resourceType: "image",
    policy: secureDefault,
    loader: mock.asLoader(),
    maxRedirects: 3,
  });
  assert.equal(result.ok, false);
  assert.match(result.error!, /too-many-redirects/);
  assert.equal(mock.attempts.length, 4);
});

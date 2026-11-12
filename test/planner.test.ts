import { test } from "node:test";
import assert from "node:assert/strict";
import { planRequest } from "../src/planner.ts";
import { mapPort } from "../src/ports.ts";
import type { DocumentPolicy } from "../src/types.ts";

const secureDefault: DocumentPolicy = {
  secureContext: true,
  upgradeInsecureRequests: false,
  blockAllMixedContent: false,
};

test("非安全文档中的明文子资源直接放行", () => {
  const plan = planRequest({
    url: "http://cdn.example.com/app.js",
    resourceType: "script",
    policy: { ...secureDefault, secureContext: false },
  });
  assert.equal(plan.action, "allow");
  assert.equal(plan.url, "http://cdn.example.com/app.js");
});

test("安全文档 + 可阻断类型（script）默认阻断", () => {
  const plan = planRequest({
    url: "http://cdn.example.com/app.js",
    resourceType: "script",
    policy: secureDefault,
  });
  assert.equal(plan.action, "block");
  assert.equal(plan.reason, "blockable-mixed-content");
});

test("安全文档 + 可选项阻断类型（image）默认自动升级", () => {
  const plan = planRequest({
    url: "http://cdn.example.com/logo.png",
    resourceType: "image",
    policy: secureDefault,
  });
  assert.equal(plan.action, "upgrade");
  assert.equal(plan.url, "https://cdn.example.com/logo.png");
});

test("upgradeInsecureRequests 策略下 script 也被升级", () => {
  const plan = planRequest({
    url: "http://cdn.example.com/app.js",
    resourceType: "script",
    policy: { ...secureDefault, upgradeInsecureRequests: true },
  });
  assert.equal(plan.action, "upgrade");
  assert.equal(plan.url, "https://cdn.example.com/app.js");
});

test("blockAllMixedContent 策略下 image 也被阻断", () => {
  const plan = planRequest({
    url: "http://cdn.example.com/logo.png",
    resourceType: "image",
    policy: { ...secureDefault, blockAllMixedContent: true },
  });
  assert.equal(plan.action, "block");
  assert.equal(plan.reason, "policy-block-all-mixed-content");
});

test("已是 https 的资源直接放行", () => {
  const plan = planRequest({
    url: "https://cdn.example.com/app.js",
    resourceType: "script",
    policy: secureDefault,
  });
  assert.equal(plan.action, "allow");
});

test("ws 升级为 wss", () => {
  const plan = planRequest({
    url: "ws://api.example.com/socket",
    resourceType: "websocket",
    policy: { ...secureDefault, upgradeInsecureRequests: true },
  });
  assert.equal(plan.action, "upgrade");
  assert.equal(plan.url, "wss://api.example.com/socket");
});

test("默认端口映射：显式 80 升级为 443", () => {
  const plan = planRequest({
    url: "http://cdn.example.com:80/logo.png",
    resourceType: "image",
    policy: secureDefault,
  });
  assert.equal(plan.url, "https://cdn.example.com/logo.png");
});

test("自定义端口映射表生效，未映射端口原样保留", () => {
  const plan = planRequest({
    url: "http://cdn.example.com:8080/logo.png",
    resourceType: "image",
    policy: secureDefault,
    portMap: { 80: 443, 8080: 8443 },
  });
  assert.equal(plan.url, "https://cdn.example.com:8443/logo.png");

  const kept = planRequest({
    url: "http://cdn.example.com:9999/logo.png",
    resourceType: "image",
    policy: secureDefault,
  });
  assert.equal(kept.url, "https://cdn.example.com:9999/logo.png");
});

test("mapPort：null 端口保持 null，未映射端口保留", () => {
  assert.equal(mapPort(null), null);
  assert.equal(mapPort(80), 443);
  assert.equal(mapPort(1234), 1234);
  assert.equal(mapPort(8080, { 8080: 8443 }), 8443);
});

test("内网地址不豁免：私有 IP 与内网主机名同样升级/阻断", () => {
  const upgraded = planRequest({
    url: "http://192.168.1.10/logo.png",
    resourceType: "image",
    policy: secureDefault,
  });
  assert.equal(upgraded.action, "upgrade");
  assert.equal(upgraded.url, "https://192.168.1.10/logo.png");

  const blocked = planRequest({
    url: "http://intranet.internal/app.js",
    resourceType: "script",
    policy: secureDefault,
  });
  assert.equal(blocked.action, "block");
});

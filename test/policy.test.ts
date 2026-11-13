import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { createRequestPolicy } from "../src/policy.ts";
import type { AddressResolver, RequestInfo } from "../src/policy.ts";

const PAGE = "https://docs.example.com/render/input";

function resolver(records: Record<string, string[]>): AddressResolver {
  return async (hostname) => {
    const addresses = records[hostname];
    if (!addresses) {
      throw new Error(`ENOTFOUND ${hostname}`);
    }
    return addresses;
  };
}

const defaultResolver: AddressResolver = resolver({
  "docs.example.com": ["93.184.10.2"],
  "cdn.example.net": ["104.16.12.34"],
  "evil.test": ["169.254.169.254"],
  "outside.example.net": ["93.184.216.34"],
  "split.example": ["93.184.10.2", "10.0.0.9"],
  "assets.trusted.example.org": ["93.184.10.8"],
});

function policy(
  overrides: Partial<Parameters<typeof createRequestPolicy>[0]> = {},
) {
  return createRequestPolicy({
    resolveAddresses: defaultResolver,
    allowedHosts: ["docs.example.com", ".trusted.example.org"],
    ...overrides,
  });
}

function request(overrides: Partial<RequestInfo>): RequestInfo {
  return {
    url: PAGE,
    type: "document",
    initiatorFrameUrl: null,
    ...overrides,
  };
}

describe("题目要求的攻击场景", () => {
  it("拒绝 file:// iframe（本地文件协议）", async () => {
    const decision = await policy().decide(
      request({
        url: "file:///etc/passwd",
        type: "document",
        initiatorFrameUrl: PAGE,
      }),
    );
    assert.equal(decision.allowed, false);
    if (!decision.allowed) {
      assert.equal(decision.reason, "scheme-denied");
    }
  });

  it("拒绝重定向到云元数据 169.254.169.254", async () => {
    const decision = await policy().decide(
      request({
        url: "https://docs.example.com/redirect-to-metadata",
        type: "document",
        redirectChain: [
          "https://docs.example.com/start",
          "http://169.254.169.254/latest/meta-data/",
        ],
      }),
    );
    assert.equal(decision.allowed, false);
    if (!decision.allowed) {
      assert.equal(decision.reason, "private-address");
      assert.equal(
        decision.url,
        "http://169.254.169.254/latest/meta-data/",
      );
      assert.match(decision.detail ?? "", /169\.254\.169\.254/);
    }
  });

  it("按字面拒绝十进制整数内网 IP", async () => {
    const decision = await policy().decide(
      request({
        url: "http://2130706433/secret",
        type: "fetch",
        initiatorFrameUrl: PAGE,
      }),
    );
    assert.equal(decision.allowed, false);
    if (!decision.allowed) {
      assert.equal(decision.reason, "private-address");
    }
  });

  it("拒绝 data:text/html 文档注入", async () => {
    const decision = await policy().decide(
      request({
        url: "data:text/html,<script>fetch('http://evil.test')</script>",
        type: "document",
        initiatorFrameUrl: PAGE,
      }),
    );
    assert.equal(decision.allowed, false);
    if (!decision.allowed) {
      assert.equal(decision.reason, "data-type-denied");
    }
  });

  it("放行合法 CDN 图片（子资源可为任意公网主机）", async () => {
    const decision = await policy().decide(
      request({
        url: "https://cdn.example.net/logo.png",
        type: "image",
        initiatorFrameUrl: PAGE,
      }),
    );
    assert.deepEqual(decision, { allowed: true });
  });
});

describe("协议门", () => {
  const sub = (url: string, type: RequestInfo["type"] = "other") =>
    policy().decide(request({ url, type, initiatorFrameUrl: PAGE }));

  it("拒绝 chrome、view-source、ftp 与 websocket 协议", async () => {
    for (const url of [
      "chrome://settings",
      "view-source:https://docs.example.com/",
      "ftp://docs.example.com/file.txt",
    ]) {
      const decision = await sub(url);
      assert.equal(decision.allowed, false, url);
      if (!decision.allowed) {
        assert.equal(decision.reason, "scheme-denied");
      }
    }

    const ws = await sub("ws://docs.example.com/socket", "websocket");
    assert.equal(ws.allowed, false);
    if (!ws.allowed) {
      assert.equal(ws.reason, "websocket-denied");
    }
  });

  it("EventSource 资源类型一律拒绝", async () => {
    const decision = await sub("https://cdn.example.net/stream", "eventsource");
    assert.equal(decision.allowed, false);
    if (!decision.allowed) {
      assert.equal(decision.reason, "eventsource-denied");
    }
  });

  it("data URI 只放行图片与字体 MIME", async () => {
    const allowedImage = await sub(
      "data:image/png;base64,AAAA",
      "image",
    );
    assert.equal(allowedImage.allowed, true);

    const allowedFont = await sub(
      "data:font/woff2;base64,AAAA",
      "font",
    );
    assert.equal(allowedFont.allowed, true);

    const svgAsDocument = await sub(
      "data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg'/>",
      "document",
    );
    assert.equal(svgAsDocument.allowed, false);
    if (!svgAsDocument.allowed) {
      assert.equal(svgAsDocument.reason, "data-type-denied");
    }

    const htmlAsImageType = await sub("data:text/html,abc", "image");
    assert.equal(htmlAsImageType.allowed, false);
  });

  it("blob 仅允许本页同源创建", async () => {
    const sameOrigin = await sub(
      "blob:https://docs.example.com/uuid-1",
      "image",
    );
    assert.equal(sameOrigin.allowed, true);

    const crossOrigin = await sub(
      "blob:https://evil.test/uuid-2",
      "image",
    );
    assert.equal(crossOrigin.allowed, false);
    if (!crossOrigin.allowed) {
      assert.equal(crossOrigin.reason, "blob-cross-origin");
    }

    const noInitiator = await policy().decide(
      request({ url: "blob:https://docs.example.com/uuid-3", type: "image" }),
    );
    assert.equal(noInitiator.allowed, false);
  });
});

describe("主机与白名单", () => {
  it("主页面必须命中白名单，支持子域通配", async () => {
    const allowed = await policy().decide(
      request({ url: "https://docs.example.com/page" }),
    );
    assert.equal(allowed.allowed, true);

    const subdomain = await policy().decide(
      request({ url: "https://assets.trusted.example.org/a" }),
    );
    assert.equal(subdomain.allowed, true);

    const denied = await policy().decide(
      request({ url: "https://outside.example.net/page" }),
    );
    assert.equal(denied.allowed, false);
    if (!denied.allowed) {
      assert.equal(denied.reason, "hostname-not-allowed");
    }
  });

  it("子资源可以是任意公网主机", async () => {
    const decision = await policy().decide(
      request({
        url: "https://cdn.example.net/app.js",
        type: "script",
        initiatorFrameUrl: PAGE,
      }),
    );
    assert.equal(decision.allowed, true);
  });

  it("DNS 解析结果中只要有一个内网地址即拒绝（全部地址须公网）", async () => {
    const decision = await policy().decide(
      request({
        url: "https://split.example/api",
        type: "fetch",
        initiatorFrameUrl: PAGE,
      }),
    );
    assert.equal(decision.allowed, false);
    if (!decision.allowed) {
      assert.equal(decision.reason, "private-address");
    }
  });

  it("解析失败按失败关闭处理", async () => {
    const decision = await policy().decide(
      request({
        url: "https://missing.example/x",
        type: "fetch",
        initiatorFrameUrl: PAGE,
      }),
    );
    assert.equal(decision.allowed, false);
    if (!decision.allowed) {
      assert.equal(decision.reason, "resolve-failed");
    }
  });
});

describe("重定向", () => {
  it("逐跳重新判定，问题跳返回其 URL", async () => {
    const decision = await policy().decide(
      request({
        url: "https://cdn.example.net/final",
        type: "document",
        redirectChain: [
          "https://docs.example.com/start",
          "file:///etc/passwd",
        ],
      }),
    );
    assert.equal(decision.allowed, false);
    if (!decision.allowed) {
      assert.equal(decision.reason, "scheme-denied");
      assert.equal(decision.url, "file:///etc/passwd");
    }
  });

  it("总跳数（含当前请求）不超过 5", async () => {
    const fourHops = [
      "https://docs.example.com/r1",
      "https://docs.example.com/r2",
      "https://docs.example.com/r3",
      "https://docs.example.com/r4",
    ];
    const ok = await policy().decide(
      request({ url: "https://docs.example.com/r5", redirectChain: fourHops }),
    );
    assert.equal(ok.allowed, true);

    const tooMany = await policy().decide(
      request({
        url: "https://docs.example.com/r6",
        redirectChain: [...fourHops, "https://docs.example.com/r5"],
      }),
    );
    assert.equal(tooMany.allowed, false);
    if (!tooMany.allowed) {
      assert.equal(tooMany.reason, "too-many-redirects");
    }
  });

  it("重定向跳到白名单外主机时主页面请求仍被拒绝", async () => {
    const decision = await policy().decide(
      request({
        url: "https://outside.example.net/landing",
        redirectChain: ["https://docs.example.com/start"],
      }),
    );
    assert.equal(decision.allowed, false);
    if (!decision.allowed) {
      assert.equal(decision.reason, "hostname-not-allowed");
    }
  });
});

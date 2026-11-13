import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { createRequestPolicy } from "../src/policy.ts";
import { playwrightRequestHandler, puppeteerRequestHandler } from "../src/adapters.ts";

const policy = createRequestPolicy({
  resolveAddresses: async (hostname) =>
    hostname === "cdn.example.net" ? ["104.16.1.1"] : ["93.184.10.2"],
  allowedHosts: ["docs.example.com"],
});

function fakeRequest(init: {
  url: string;
  resourceType: string;
  frameUrl: string | null;
  navigation?: boolean;
  redirects?: string[];
}) {
  return {
    url: () => init.url,
    resourceType: () => init.resourceType,
    frame: () => (init.frameUrl === null ? null : { url: () => init.frameUrl }),
    isNavigationRequest: () => init.navigation ?? false,
    redirectChain: () =>
      (init.redirects ?? []).map((url) => ({ url: () => url })),
  };
}

describe("Playwright 适配器", () => {
  it("放行主页面导航与公网图片子资源", async () => {
    const handler = playwrightRequestHandler(policy);
    const main = await handler(
      fakeRequest({
        url: "https://docs.example.com/",
        resourceType: "document",
        frameUrl: null,
        navigation: true,
      }),
    );
    assert.equal(main, true);

    const image = await handler(
      fakeRequest({
        url: "https://cdn.example.net/a.png",
        resourceType: "image",
        frameUrl: "https://docs.example.com/",
      }),
    );
    assert.equal(image, true);
  });

  it("拦截 file iframe 与 WebSocket", async () => {
    const handler = playwrightRequestHandler(policy);
    const fileIframe = await handler(
      fakeRequest({
        url: "file:///etc/passwd",
        resourceType: "document",
        frameUrl: "https://docs.example.com/",
      }),
    );
    assert.equal(fileIframe, false);

    const websocket = await handler(
      fakeRequest({
        url: "wss://docs.example.com/ws",
        resourceType: "websocket",
        frameUrl: "https://docs.example.com/",
      }),
    );
    assert.equal(websocket, false);
  });
});

describe("Puppeteer 适配器", () => {
  it("归一化 img/font_face 等资源类型名并透传重定向链", async () => {
    const handler = puppeteerRequestHandler(policy);
    const redirectedToMetadata = await handler(
      fakeRequest({
        url: "http://169.254.169.254/latest/meta-data/",
        resourceType: "document",
        frameUrl: null,
        navigation: true,
        redirects: ["https://docs.example.com/start"],
      }),
    );
    assert.equal(redirectedToMetadata, false);
  });
});

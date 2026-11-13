import { describe, it } from "node:test";
import assert from "node:assert/strict";

import { assembleRequest } from "../index.ts";

const DOC = "https://shop.example/checkout";

describe("assembleRequest active content", () => {
  it("blocks an http script without invoking any probe", async () => {
    let probed = false;
    const decision = await assembleRequest({
      documentUrl: DOC,
      resourceUrl: "http://cdn.example/app.js",
      kind: "script",
      probe: async () => {
        probed = true;
        return true;
      },
    });
    assert.equal(decision.allowed, false);
    assert.equal(probed, false);
    if (!decision.allowed) assert.equal(decision.reason, "active-mixed-content");
  });

  it("blocks a ws WebSocket", async () => {
    const decision = await assembleRequest({
      documentUrl: DOC,
      resourceUrl: "ws://socket.example/stream",
      kind: "websocket",
      probe: async () => true,
    });
    assert.equal(decision.allowed, false);
    if (!decision.allowed) assert.equal(decision.reason, "active-mixed-content");
  });

  it("passes secure https scripts straight through", async () => {
    const decision = await assembleRequest({
      documentUrl: DOC,
      resourceUrl: "https://cdn.example/app.js",
      kind: "script",
    });
    assert.equal(decision.allowed, true);
    if (decision.allowed) {
      assert.equal(decision.upgraded, false);
      assert.equal(decision.url.protocol, "https:");
    }
  });
});

describe("assembleRequest image upgrade", () => {
  it("uses the upgraded https URL when the probe succeeds", async () => {
    const seen: string[] = [];
    const decision = await assembleRequest({
      documentUrl: DOC,
      resourceUrl: "http://img.example/cat.png",
      kind: "image",
      probe: async (url) => {
        seen.push(url.href);
        return true;
      },
    });
    assert.equal(decision.allowed, true);
    assert.deepEqual(seen, ["https://img.example/cat.png"]);
    if (decision.allowed) {
      assert.equal(decision.url.href, "https://img.example/cat.png");
      assert.equal(decision.upgraded, true);
    }
  });

  it("fails the image when the upgrade probe fails and never falls back", async () => {
    const seen: string[] = [];
    const decision = await assembleRequest({
      documentUrl: DOC,
      resourceUrl: "http://img.example/missing.png",
      kind: "image",
      probe: async (url) => {
        seen.push(url.href);
        return false;
      },
    });
    assert.equal(decision.allowed, false);
    if (!decision.allowed) {
      assert.equal(decision.reason, "upgrade-failed");
      assert.equal(decision.originalUrl, "http://img.example/missing.png");
      assert.equal(decision.upgradedUrl?.href, "https://img.example/missing.png");
    }
    // Only the secure URL was ever touched.
    assert.deepEqual(seen, ["https://img.example/missing.png"]);
    assert.ok(!seen.some((href) => href.startsWith("http:")));
  });

  it("does not probe images that were already https", async () => {
    let probed = false;
    const decision = await assembleRequest({
      documentUrl: DOC,
      resourceUrl: "https://img.example/cat.png",
      kind: "image",
      probe: async () => {
        probed = true;
        return true;
      },
    });
    assert.equal(decision.allowed, true);
    assert.equal(probed, false);
  });

  it("handles relative http image URLs resolved via the document", async () => {
    const decision = await assembleRequest({
      documentUrl: "http://shop.example/page",
      isSecureContext: true,
      resourceUrl: "/img/cat.png",
      kind: "image",
      probe: async () => true,
    });
    assert.equal(decision.allowed, true);
    if (decision.allowed) {
      assert.equal(decision.url.href, "https://shop.example/img/cat.png");
    }
  });
});

describe("assembleRequest non-image passive kinds", () => {
  it("upgrades an http font without probing", async () => {
    let probed = false;
    const decision = await assembleRequest({
      documentUrl: DOC,
      resourceUrl: "http://cdn.example/font.woff2",
      kind: "font",
      probe: async () => {
        probed = true;
        return true;
      },
    });
    assert.equal(decision.allowed, true);
    assert.equal(probed, false);
    if (decision.allowed) assert.equal(decision.url.protocol, "https:");
  });
});

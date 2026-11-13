import { describe, it } from "node:test";
import assert from "node:assert/strict";

import { decideMixedContent, isBlocked } from "../index.ts";

const DOC = "https://shop.example/checkout";

describe("active mixed content on a secure document", () => {
  it("blocks an http script", () => {
    const decision = decideMixedContent({
      documentUrl: DOC,
      resourceUrl: "http://cdn.example/app.js",
      kind: "script",
    });
    assert.equal(decision.allowed, false);
    if (!decision.allowed) {
      assert.equal(decision.reason, "active-mixed-content");
      assert.match(decision.originalUrl, /^http:\/\//);
    }
  });

  it("blocks an http module script", () => {
    const decision = decideMixedContent({
      documentUrl: DOC,
      resourceUrl: "http://cdn.example/app.mjs",
      kind: "module",
    });
    assert.equal(isBlocked(decision), true);
    if (!decision.allowed) assert.equal(decision.reason, "active-mixed-content");
  });

  it("blocks an http XHR request", () => {
    const decision = decideMixedContent({
      documentUrl: DOC,
      resourceUrl: "http://api.example/data",
      kind: "xhr",
    });
    assert.equal(decision.allowed, false);
    if (!decision.allowed) assert.equal(decision.reason, "active-mixed-content");
  });

  it("blocks an http fetch request", () => {
    const decision = decideMixedContent({
      documentUrl: DOC,
      resourceUrl: "http://api.example/data",
      kind: "fetch",
    });
    assert.equal(decision.allowed, false);
  });

  it("blocks a ws WebSocket", () => {
    const decision = decideMixedContent({
      documentUrl: DOC,
      resourceUrl: "ws://socket.example/stream",
      kind: "websocket",
    });
    assert.equal(decision.allowed, false);
    if (!decision.allowed) {
      assert.equal(decision.reason, "active-mixed-content");
      assert.equal(decision.originalUrl, "ws://socket.example/stream");
    }
  });

  it("lets protocol-relative active content inherit the secure https scheme", () => {
    const decision = decideMixedContent({
      documentUrl: DOC,
      resourceUrl: "//cdn.example/app.js",
      kind: "script",
    });
    assert.equal(decision.allowed, true);
    if (decision.allowed) {
      assert.equal(decision.url.href, "https://cdn.example/app.js");
    }
  });

  it("blocks relative active content when the document itself is http", () => {
    const decision = decideMixedContent({
      documentUrl: "http://shop.example/checkout",
      resourceUrl: "/app.js",
      kind: "script",
    });
    assert.equal(decision.allowed, true);
    if (decision.allowed) assert.equal(decision.url.protocol, "http:");
  });
});

describe("secure requests from a secure document", () => {
  it("allows an https script", () => {
    const decision = decideMixedContent({
      documentUrl: DOC,
      resourceUrl: "https://cdn.example/app.js",
      kind: "script",
    });
    assert.deepEqual(decision, {
      allowed: true,
      url: new URL("https://cdn.example/app.js"),
      kind: "script",
      upgraded: false,
    });
  });

  it("allows a wss WebSocket", () => {
    const decision = decideMixedContent({
      documentUrl: DOC,
      resourceUrl: "wss://socket.example/stream",
      kind: "websocket",
    });
    assert.equal(decision.allowed, true);
    if (decision.allowed) assert.equal(decision.url.protocol, "wss:");
  });

  it("allows https fetch and XHR", () => {
    for (const kind of ["fetch", "xhr"] as const) {
      const decision = decideMixedContent({
        documentUrl: DOC,
        resourceUrl: "https://api.example/data",
        kind,
      });
      assert.equal(decision.allowed, true);
    }
  });

  it("resolves relative https resources against the document", () => {
    const decision = decideMixedContent({
      documentUrl: DOC,
      resourceUrl: "/assets/app.js",
      kind: "script",
    });
    assert.equal(decision.allowed, true);
    if (decision.allowed) {
      assert.equal(decision.url.href, "https://shop.example/assets/app.js");
    }
  });

  it("allows data: and blob: resources", () => {
    for (const resourceUrl of [
      "data:text/javascript,console.log(1)",
      "blob:https://shop.example/uuid",
    ]) {
      const decision = decideMixedContent({
        documentUrl: DOC,
        resourceUrl,
        kind: "script",
      });
      assert.equal(decision.allowed, true, resourceUrl);
    }
  });
});

describe("passive content upgrades", () => {
  it("upgrades an http image to https", () => {
    const decision = decideMixedContent({
      documentUrl: DOC,
      resourceUrl: "http://img.example/cat.png",
      kind: "image",
    });
    assert.equal(decision.allowed, true);
    if (decision.allowed) {
      assert.equal(decision.upgraded, true);
      assert.equal(decision.url.protocol, "https:");
      assert.equal(decision.url.host, "img.example");
    }
  });

  it("upgrades ws-style passive URLs to wss", () => {
    const decision = decideMixedContent({
      documentUrl: DOC,
      resourceUrl: "ws://cdn.example/font.woff2",
      kind: "font",
    });
    assert.equal(decision.allowed, true);
    if (decision.allowed) assert.equal(decision.url.protocol, "wss:");
  });

  it("leaves already-secure images untouched", () => {
    const decision = decideMixedContent({
      documentUrl: DOC,
      resourceUrl: "https://img.example/cat.png",
      kind: "image",
    });
    assert.equal(decision.allowed, true);
    if (decision.allowed) assert.equal(decision.upgraded, false);
  });
});

describe("insecure documents", () => {
  it("does not apply mixed-content blocking on an http page", () => {
    const decision = decideMixedContent({
      documentUrl: "http://shop.example/page",
      resourceUrl: "http://cdn.example/app.js",
      kind: "script",
    });
    assert.equal(decision.allowed, true);
    if (decision.allowed) assert.equal(decision.url.protocol, "http:");
  });

  it("respects an explicit secure-context flag", () => {
    const decision = decideMixedContent({
      documentUrl: "http://localhost/page",
      isSecureContext: true,
      resourceUrl: "http://cdn.example/app.js",
      kind: "script",
    });
    assert.equal(decision.allowed, false);
  });
});

describe("invalid input", () => {
  it("reports invalid-url for unparseable resources", () => {
    const decision = decideMixedContent({
      documentUrl: DOC,
      resourceUrl: "http://",
      kind: "script",
    });
    assert.equal(decision.allowed, false);
    if (!decision.allowed) assert.equal(decision.reason, "invalid-url");
  });

  it("reports invalid-url when a relative URL has no usable base", () => {
    const decision = decideMixedContent({
      resourceUrl: "://missing-scheme",
      kind: "script",
    });
    assert.equal(decision.allowed, false);
    if (!decision.allowed) assert.equal(decision.reason, "invalid-url");
  });
});

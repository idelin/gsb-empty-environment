import { describe, it } from "node:test";
import assert from "node:assert/strict";

import {
  inferSecureContext,
  isPlaintextScheme,
  isSecureScheme,
  resolveUrl,
  upgradeToSecure,
} from "../index.ts";

describe("resolveUrl", () => {
  it("resolves relative URLs against a base", () => {
    assert.equal(
      resolveUrl("/a/b", "https://example.com/page").href,
      "https://example.com/a/b",
    );
  });

  it("accepts URL instances", () => {
    const base = new URL("https://example.com/page");
    assert.equal(resolveUrl(new URL("https://x.test/y"), base).href, "https://x.test/y");
  });

  it("throws on malformed URLs without a base", () => {
    assert.throws(() => resolveUrl("not a url"));
  });
});

describe("scheme predicates", () => {
  it("classifies secure and plaintext schemes", () => {
    assert.equal(isSecureScheme(new URL("https://x.test")), true);
    assert.equal(isSecureScheme(new URL("wss://x.test")), true);
    assert.equal(isSecureScheme(new URL("data:text/plain,hi")), true);
    assert.equal(isPlaintextScheme(new URL("http://x.test")), true);
    assert.equal(isPlaintextScheme(new URL("ws://x.test")), true);
    assert.equal(isPlaintextScheme(new URL("https://x.test")), false);
  });
});

describe("upgradeToSecure", () => {
  it("upgrades http to https and ws to wss", () => {
    assert.equal(
      upgradeToSecure(new URL("http://x.test/a?b=1")).href,
      "https://x.test/a?b=1",
    );
    assert.equal(
      upgradeToSecure(new URL("ws://x.test/stream")).protocol,
      "wss:",
    );
  });

  it("leaves secure URLs unchanged", () => {
    const url = new URL("https://x.test/a");
    assert.equal(upgradeToSecure(url), url);
  });
});

describe("inferSecureContext", () => {
  it("treats https and wss documents as secure", () => {
    assert.equal(inferSecureContext("https://x.test"), true);
    assert.equal(inferSecureContext("wss://x.test"), true);
    assert.equal(inferSecureContext("http://x.test"), false);
  });

  it("defaults to secure when no document is supplied", () => {
    assert.equal(inferSecureContext(), true);
  });
});

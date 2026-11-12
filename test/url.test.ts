import { describe, it } from "node:test";
import assert from "node:assert/strict";

import { SubmitError } from "../src/types.ts";
import {
  encodeFormComponent,
  parseHttpOrigin,
  resolveAction,
  sameOriginPolicy,
} from "../src/url.ts";

describe("resolveAction pins against the captured base", () => {
  it("resolves relative actions from the approval-time base", () => {
    const url = resolveAction("https://app.example.test/checkout/index.html", "/pay");
    assert.equal(url.href, "https://app.example.test/pay");
  });

  it("does not change when the live base later moves to an attacker origin", () => {
    const approvedBase = "https://app.example.test/order/1";
    const first = resolveAction(approvedBase, "charge");
    const attackerBase = "https://evil.example.test/order/1";
    const second = resolveAction(attackerBase, "charge");

    assert.equal(first.href, "https://app.example.test/order/charge");
    assert.equal(second.href, "https://evil.example.test/order/charge");
    assert.notEqual(first.href, second.href);
  });

  it("treats an empty action as the base URL itself", () => {
    const url = resolveAction("https://app.example.test/form", "");
    assert.equal(url.href, "https://app.example.test/form");
  });

  it("resolves protocol-relative and absolute actions", () => {
    assert.equal(
      resolveAction("https://app.example.test/a", "//cdn.example.test/ingest").href,
      "https://cdn.example.test/ingest",
    );
    assert.equal(
      resolveAction("https://app.example.test/a", "https://api.example.test/v1/hook").href,
      "https://api.example.test/v1/hook",
    );
  });

  it("rejects javascript: and data: actions", () => {
    assert.throws(
      () => resolveAction("https://app.example.test/", "javascript:alert(1)"),
      (error: unknown) => error instanceof SubmitError && error.code === "UNSAFE_ACTION_URL",
    );
    assert.throws(
      () => resolveAction("https://app.example.test/", "data:text/html,x"),
      (error: unknown) => error instanceof SubmitError && error.code === "UNSAFE_ACTION_URL",
    );
  });

  it("rejects a poisoned base URL", () => {
    assert.throws(
      () => resolveAction("javascript:alert(1)", "/pay"),
      (error: unknown) => error instanceof SubmitError && error.code === "UNSAFE_BASE_URL",
    );
  });
});

describe("parseHttpOrigin", () => {
  it("accepts http(s) origins and rejects others", () => {
    assert.equal(parseHttpOrigin("https://app.example.test").origin, "https://app.example.test");
    assert.throws(
      () => parseHttpOrigin("file:///etc/passwd"),
      (error: unknown) => error instanceof SubmitError && error.code === "UNSAFE_ACTION_URL",
    );
    assert.throws(
      () => parseHttpOrigin("not a url"),
      (error: unknown) => error instanceof SubmitError,
    );
  });

  it("compares origins including port", () => {
    assert.equal(
      sameOriginPolicy(
        "https://app.example.test",
        new URL("https://app.example.test:8443/pay"),
      ),
      false,
    );
    assert.equal(
      sameOriginPolicy("https://app.example.test", new URL("https://app.example.test/pay")),
      true,
    );
  });
});

describe("encodeFormComponent", () => {
  it("encodes spaces as plus and keeps the form-urlencoded safe set", () => {
    assert.equal(encodeFormComponent("a b"), "a+b");
    assert.equal(encodeFormComponent("a&b=c"), "a%26b%3Dc");
    assert.equal(encodeFormComponent("中国"), encodeURIComponent("中国"));
  });
});

import assert from "node:assert/strict";
import test from "node:test";
import {
  checkRedirectUri,
  inspectRedirectUri,
  isRedirectUriAllowed,
  matchRedirectUri,
  normalizeRedirectUri,
  parseRedirectUri,
  RedirectUriError,
  validateRedirectUri,
} from "../index.ts";

const REGISTERED = "https://app.example.com/oauth/callback";

test("accepts only the exact registered callback", () => {
  assert.equal(validateRedirectUri(REGISTERED, REGISTERED), true);
  assert.equal(
    matchRedirectUri(REGISTERED, "https://APP.EXAMPLE.COM/oauth/callback"),
    REGISTERED,
  );
});

test("rejects suffix-domain and subdomain lookalikes", () => {
  for (const candidate of [
    "https://app.example.com.attacker.io/oauth/callback",
    "https://app.example.com.attacker.io.evil.test/oauth/callback",
    "https://evil-app.example.com/oauth/callback",
    "https://app.example.com.evil.test/oauth/callback",
    "https://attacker.io/app.example.com/oauth/callback",
  ]) {
    assert.equal(validateRedirectUri(REGISTERED, candidate), false, candidate);
  }
});

test("rejects user info, fragments, and ambiguous separators", () => {
  for (const candidate of [
    "https://user@app.example.com/oauth/callback",
    "https://user:pass@app.example.com/oauth/callback",
    "https://@app.example.com/oauth/callback",
    "https://app.example.com/oauth/callback#fragment",
    "https://app.example.com/oauth/callback#",
    "https://app.example.com\\@attacker.io/oauth/callback",
    "https:///app.example.com/oauth/callback",
    "https:////app.example.com/oauth/callback",
  ]) {
    assert.equal(validateRedirectUri(REGISTERED, candidate), false, candidate);
  }
});

test("rejects path traversal and encoded path separators", () => {
  for (const candidate of [
    "https://app.example.com/oauth/../oauth/callback",
    "https://app.example.com/oauth/%2e%2e/oauth/callback",
    "https://app.example.com/oauth/%2E%2E/oauth/callback",
    "https://app.example.com/oauth/%252e%252e/oauth/callback",
    "https://app.example.com/oauth/%25252e%25252e/oauth/callback",
    "https://app.example.com/oauth/callback%2f..%2fadmin",
    "https://app.example.com/oauth/callback%5c..%5cadmin",
  ]) {
    assert.equal(validateRedirectUri(REGISTERED, candidate), false, candidate);
  }
});

test("normalizes default ports but still compares effective ports exactly", () => {
  assert.equal(
    validateRedirectUri(REGISTERED, "https://app.example.com:443/oauth/callback"),
    true,
  );
  assert.equal(
    normalizeRedirectUri("https://app.example.com:443/oauth/callback"),
    REGISTERED,
  );

  const registeredCustomPort = "https://app.example.com:8443/oauth/callback";
  assert.equal(
    validateRedirectUri(registeredCustomPort, "https://app.example.com/oauth/callback"),
    false,
  );
  assert.equal(
    validateRedirectUri(registeredCustomPort, "https://app.example.com:8443/oauth/callback"),
    true,
  );
});

test("normalizes IDNA hosts and rejects IDN confusables", () => {
  const registeredUnicode = "https://münich.example/callback";
  assert.equal(
    validateRedirectUri(registeredUnicode, "https://xn--mnich-kva.example/callback"),
    true,
  );
  assert.equal(
    normalizeRedirectUri(registeredUnicode),
    "https://xn--mnich-kva.example/callback",
  );

  // Cyrillic "а" is visually similar to ASCII "a" but a different host.
  assert.equal(
    validateRedirectUri(REGISTERED, "https://\u0430pp.example.com/oauth/callback"),
    false,
  );
  assert.equal(
    parseRedirectUri("https://\u0430pp.example.com/oauth/callback").host,
    "xn--pp-6kc.example.com",
  );
});

test("path wildcards match exactly one declared segment and cannot cross directories", () => {
  const template = "https://app.example.com/oauth/*/callback";
  assert.equal(
    matchRedirectUri(template, "https://app.example.com/oauth/tenant-42/callback"),
    "https://app.example.com/oauth/tenant-42/callback",
  );
  for (const candidate of [
    "https://app.example.com/oauth/tenant-42/extra/callback",
    "https://app.example.com/oauth//callback",
    "https://app.example.com/oauth/%2e%2e/callback",
    "https://app.example.com/oauth/a%2fb/callback",
    "https://app.example.com/oauth/a%252fb/callback",
  ]) {
    assert.equal(validateRedirectUri(template, candidate), false, candidate);
  }

  const inferredWildcard = "https://app.example.com/oauth/tenant-42/callback";
  assert.equal(
    validateRedirectUri(inferredWildcard, "https://app.example.com/oauth/tenant-43/callback"),
    false,
  );

  assert.throws(
    () =>
      parseRedirectUri(
        "https://app.example.com/oauth/tenant*/callback",
      ),
    (error: unknown) =>
      error instanceof RedirectUriError && error.code === "invalid_redirect_uri",
  );
  assert.throws(
    () => parseRedirectUri("https://app.example.com/oauth/**/callback"),
    RedirectUriError,
  );
});

test("requires exact query matching", () => {
  const registered = "https://app.example.com/callback?tenant=one";
  assert.equal(
    validateRedirectUri(registered, "https://app.example.com/callback?tenant=one"),
    true,
  );
  assert.equal(
    validateRedirectUri(registered, "https://app.example.com/callback?tenant=two"),
    false,
  );
  assert.equal(
    validateRedirectUri(registered, "https://app.example.com/callback?tenant=one&x=1"),
    false,
  );
});

test("loopback redirect URIs require explicit authorization", () => {
  assert.throws(
    () => parseRedirectUri("http://127.0.0.1:3000/callback"),
    (error: unknown) =>
      error instanceof RedirectUriError && error.code === "loopback_not_allowed",
  );

  for (const loopback of [
    "http://127.0.0.1:3000/callback",
    "http://localhost:3000/callback",
    "http://[::1]:3000/callback",
  ]) {
    assert.equal(parseRedirectUri(loopback, { allowLoopback: true }).uri, loopback);
  }

  assert.throws(
    () =>
      parseRedirectUri("http://127.0.0.1.attacker.io:3000/callback", {
        allowLoopback: true,
      }),
    RedirectUriError,
  );
  assert.throws(
    () =>
      parseRedirectUri("http://10.0.0.1:3000/callback", {
        allowLoopback: true,
      }),
    RedirectUriError,
  );
});

test("custom schemes require an explicit scheme allowlist", () => {
  assert.throws(
    () => parseRedirectUri("com.example.app://oauth/callback"),
    (error: unknown) =>
      error instanceof RedirectUriError && error.code === "custom_scheme_not_allowed",
  );

  const policy = { allowCustomSchemes: ["com.example.app"] };
  const registered = "com.example.app://oauth/callback";
  assert.equal(parseRedirectUri(registered, policy).uri, registered);
  assert.equal(
    validateRedirectUri(registered, "com.example.app:/oauth/callback", policy),
    false,
  );
  assert.equal(
    validateRedirectUri(registered, "com.example.app.attacker://oauth/callback", policy),
    false,
  );
  assert.equal(
    validateRedirectUri(registered, "other.app://oauth/callback", policy),
    false,
  );
  assert.throws(
    () =>
      parseRedirectUri("javascript:alert(1)", {
        allowCustomScheme: true,
      }),
    RedirectUriError,
  );
});

test("supports hostless custom-scheme redirect URIs when authorized", () => {
  const policy = { allowCustomSchemes: ["com.example.app"] };
  const registered = "com.example.app:/oauth2redirect";
  assert.equal(parseRedirectUri(registered, policy).host, "");
  assert.equal(validateRedirectUri(registered, registered, policy), true);
});

test("returns structured errors without leaking a fallback redirect", () => {
  const result = checkRedirectUri(
    REGISTERED,
    "https://app.example.com.attacker.io/oauth/callback",
  );
  assert.equal(result.ok, false);
  assert.equal(result.redirectUri, undefined);
  assert.equal(result.error?.code, "invalid_redirect_uri");
  assert.equal(
    isRedirectUriAllowed(REGISTERED, "https://app.example.com.attacker.io/oauth/callback"),
    false,
  );
});

test("publishes canonical parsed components", () => {
  const parsed = inspectRedirectUri(
    "HTTPS://APP.EXAMPLE.COM:443/oauth/callback?tenant=one",
  );
  assert.equal(parsed.scheme, "https");
  assert.equal(parsed.host, "app.example.com");
  assert.equal(parsed.port, 443);
  assert.equal(parsed.path, "/oauth/callback");
  assert.equal(parsed.query, "?tenant=one");
  assert.equal(parsed.uri, "https://app.example.com/oauth/callback?tenant=one");
});

test("rejects wildcard characters in hosts", () => {
  assert.throws(
    () => parseRedirectUri("https://*.example.com/callback"),
    RedirectUriError,
  );
});

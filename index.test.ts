import assert from "node:assert/strict";
import test from "node:test";

import {
  HstsStore,
  canonicalizeHost,
  parseHstsHeader,
  readHstsHeader,
} from "./index.ts";

const sts = (value: string) => ({ "strict-transport-security": value });

test("parses case-insensitive directive names and decimal max-age", () => {
  assert.deepEqual(
    parseHstsHeader("MAX-AGE = 0003600 , IncludeSubDomains"),
    { maxAge: 3600, includeSubDomains: true },
  );
  assert.deepEqual(
    parseHstsHeader("max-age=0; unknown=value"),
    null,
  );
  assert.deepEqual(
    parseHstsHeader("max-age=12, unknown=value, another"),
    { maxAge: 12, includeSubDomains: false },
  );
});

test("rejects non-decimal or malformed max-age values", () => {
  for (const value of [
    "max-age",
    "max-age=",
    "max-age=-1",
    "max-age=+1",
    "max-age=1.5",
    "max-age=1e3",
    "max-age=\"10\"",
    "max-age=10 20",
    "includeSubDomains",
    "",
  ]) {
    assert.equal(parseHstsHeader(value), null, value);
  }
});

test("a duplicate directive invalidates the whole field", () => {
  assert.equal(parseHstsHeader("max-age=10, MAX-AGE=20"), null);
  assert.equal(
    parseHstsHeader("includeSubDomains, includeSubDomains, max-age=10"),
    null,
  );
  assert.equal(parseHstsHeader("unknown=1, UNKNOWN=2, max-age=10"), null);

  const store = new HstsStore();
  assert.equal(
    store.processResponse(
      "https://example.com/",
      sts("max-age=3600, MAX-AGE=60"),
    ),
    false,
  );
  assert.equal(store.isSecure("example.com"), false);
});

test("an invalid duplicate does not overwrite an existing policy", () => {
  const store = new HstsStore({ now: () => 1_000 });
  assert.equal(
    store.processResponse("https://example.com/", sts("max-age=3600")),
    true,
  );

  assert.equal(
    store.processResponse(
      "https://example.com/",
      sts("max-age=10, Max-Age=20, INCLUDE-SUBDOMAINS"),
    ),
    false,
  );

  assert.deepEqual(store.getEntry("example.com"), {
    hostname: "example.com",
    maxAge: 3600,
    includeSubDomains: false,
    expiresAt: 3_601_000,
  });
  assert.equal(store.isSecure("www.example.com"), false);
});

test("duplicate zero max-age directives cannot delete an existing policy", () => {
  const store = new HstsStore({ now: () => 1_000 });
  store.processResponse("https://example.com/", sts("max-age=3600"));

  assert.equal(
    store.processResponse(
      "https://example.com/",
      sts("max-age=0, MAX-AGE=0"),
    ),
    false,
  );
  assert.equal(store.isSecure("example.com"), true);
  assert.equal(store.getEntry("example.com")?.maxAge, 3600);
});

test("plaintext HTTP headers cannot add or delete HSTS policy", () => {
  const store = new HstsStore({ now: () => 1_000 });

  assert.equal(
    store.processResponse(
      "http://plain.example.com/",
      sts("max-age=3600, includeSubDomains"),
    ),
    false,
  );
  assert.equal(store.isSecure("plain.example.com"), false);

  assert.equal(
    store.processResponse(
      "https://protected.example.com/",
      sts("max-age=3600, includeSubDomains"),
    ),
    true,
  );

  assert.equal(
    store.processResponse(
      "http://protected.example.com/",
      sts("max-age=0"),
    ),
    false,
  );
  assert.equal(store.isSecure("protected.example.com"), true);
  assert.equal(store.isSecure("www.protected.example.com"), true);
});

test("includeSubDomains matches only complete DNS labels", () => {
  const store = new HstsStore({ now: () => 1_000 });
  store.processResponse(
    "https://example.com/",
    sts("max-age=3600, includeSubDomains"),
  );

  for (const host of [
    "example.com",
    "www.example.com",
    "deep.api.example.com",
    "www.example.com.",
  ]) {
    assert.equal(store.isSecure(host), true, host);
  }

  for (const host of [
    "notexample.com",
    "badexample.com",
    "example.com.attacker.test",
    "www.notexample.com",
    "example-com.attacker.test",
  ]) {
    assert.equal(store.isSecure(host), false, host);
  }
});

test("subdomain deletion removes only its own exact entry", () => {
  const store = new HstsStore({ now: () => 1_000 });
  store.processResponse(
    "https://example.com/",
    sts("max-age=3600, includeSubDomains"),
  );
  store.processResponse(
    "https://api.example.com/",
    sts("max-age=60, includeSubDomains"),
  );

  assert.equal(store.isSecure("x.api.example.com"), true);
  assert.equal(
    store.processResponse("https://api.example.com/", sts("max-age=0")),
    true,
  );

  assert.equal(store.getEntry("api.example.com"), null);
  assert.equal(store.isSecure("api.example.com"), true);
  assert.equal(store.isSecure("other.example.com"), true);
  assert.equal(store.isSecure("x.api.example.com"), true);
});

test("deleting a child policy affects only that child and its descendants", () => {
  const store = new HstsStore({ now: () => 1_000 });
  store.processResponse(
    "https://api.example.com/",
    sts("max-age=3600, includeSubDomains"),
  );

  assert.equal(store.isSecure("x.api.example.com"), true);
  store.processResponse("https://api.example.com/", sts("max-age=0"));

  assert.equal(store.getEntry("api.example.com"), null);
  assert.equal(store.isSecure("api.example.com"), false);
  assert.equal(store.isSecure("x.api.example.com"), false);
  assert.equal(store.isSecure("example.com"), false);
});

test("IP literal hosts are ignored", () => {
  const store = new HstsStore();
  const ipv4Header = sts("max-age=3600");

  for (const url of [
    "https://127.0.0.1/",
    "https://127.1/",
    "https://0x7f000001/",
    "https://[::1]/",
    "https://[2001:db8::1]/",
  ]) {
    assert.equal(store.processResponse(url, ipv4Header), false, url);
  }

  assert.equal(store.size, 0);
  assert.equal(store.isSecure("127.0.0.1"), false);
  assert.equal(store.isSecure("::1"), false);
});

test("max-age=0 deletes the exact entry with normal browser expiration", () => {
  let now = 1_000;
  const store = new HstsStore({ now: () => now });
  store.processResponse("https://example.com/", sts("max-age=2"));

  now = 2_999;
  assert.equal(store.isSecure("example.com"), true);
  now = 3_000;
  assert.equal(store.isSecure("example.com"), false);
  assert.equal(store.getEntry("example.com"), null);
  assert.equal(store.size, 0);
});

test("header lookup accepts Headers, maps, arrays and mixed-case records", () => {
  assert.equal(
    readHstsHeader(new Headers({ "Strict-Transport-Security": "max-age=10" })),
    "max-age=10",
  );
  assert.equal(
    readHstsHeader(new Map([["STRICT-TRANSPORT-SECURITY", "max-age=20"]])),
    "max-age=20",
  );
  assert.equal(
    readHstsHeader([
      ["Strict-Transport-Security", "max-age=30"],
      ["content-type", "text/html"],
    ] as const),
    "max-age=30",
  );

  const store = new HstsStore();
  assert.equal(
    store.processResponse(
      "https://example.com/",
      { "Strict-Transport-Security": "MAX-AGE=40, INCLUDESUBDOMAINS" },
    ),
    true,
  );
});

test("canonicalization normalizes host case, ports and trailing dots", () => {
  assert.equal(canonicalizeHost("HTTPS://EXAMPLE.COM:443/path"), "example.com");
  assert.equal(canonicalizeHost("Example.COM."), "example.com");
  assert.equal(canonicalizeHost("https://[::1]/"), "::1");
});

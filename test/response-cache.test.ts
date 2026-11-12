import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  OPAQUE_ORIGIN,
  originOf,
  ResponseCache,
  sameOrigin,
} from "../src/index.ts";
import type {
  CacheEntryInput,
  CachedResponse,
  IntegrityMetadata,
  StructuredRequest,
} from "../src/index.ts";

const APP = "https://app.example";
const CDN = "https://cdn.example.net";

const SRI: IntegrityMetadata = {
  algorithm: "sha384",
  digest: "abc123ABC+/==",
};

function basicPut(
  cache: ResponseCache,
  overrides: Partial<CacheEntryInput<string>> &
    Pick<CacheEntryInput<string>, "request">,
): CachedResponse<string> {
  const input: CacheEntryInput<string> = {
    status: 200,
    statusText: "OK",
    headers: { "content-type": "text/plain" },
    body: "payload",
    type: "basic",
    credentialsMode: "same-origin",
    ...overrides,
  };
  return cache.put(input);
}

describe("origin model", () => {
  it("serializes tuple origins with default ports and compares them", () => {
    assert.equal(originOf("https://app.example/index.html"), "https://app.example:443");
    assert.equal(originOf("http://app.example:8080/x"), "http://app.example:8080");
    assert.equal(
      sameOrigin(originOf("https://app.example/a"), originOf("https://app.example/b")),
      true,
    );
    assert.equal(
      sameOrigin(originOf("https://app.example"), originOf("https://cdn.example.net")),
      false,
    );
  });

  it("treats opaque origins as never same-origin, even with each other", () => {
    assert.equal(sameOrigin(OPAQUE_ORIGIN, OPAQUE_ORIGIN), false);
    assert.equal(originOf("file:///etc/hosts"), OPAQUE_ORIGIN);
    assert.throws(() => originOf("not-a-url"), TypeError);
  });

  it("strips fragments from cache keys", () => {
    const cache = new ResponseCache({ version: "v1" });
    basicPut(cache, {
      request: { url: `${APP}/main.js#part1` },
      body: "js",
    });
    assert.ok(cache.match({ url: `${APP}/main.js#part2` }));
  });
});

describe("cross-origin redirects", () => {
  it("stores the final origin of a redirected CORS response", () => {
    const cache = new ResponseCache({ version: "v1" });
    const request: StructuredRequest = {
      url: `${APP}/redirect`,
      mode: "cors",
      credentials: "omit",
    };

    const stored = basicPut(cache, {
      request,
      responseUrl: `${CDN}/asset`,
      type: "cors",
      credentialsMode: "omit",
      integrityState: "verified",
      integrityMetadata: [SRI],
    });

    assert.equal(stored.origin, originOf(CDN));
    assert.equal(stored.url, `${CDN}/asset`);

    const hit = cache.inspect<string>(request);
    assert.equal(hit.matched, true);
    if (hit.matched) {
      assert.equal(hit.response.body, "payload");
    }
  });

  it("refuses to serve a same-origin basic entry through a cross-origin URL", () => {
    const cache = new ResponseCache({ version: "v1" });
    basicPut(cache, {
      request: { url: `${APP}/private`, mode: "same-origin" },
      type: "basic",
    });

    const miss = cache.inspect({ url: `${CDN}/private`, mode: "cors" });
    assert.equal(miss.matched, false);
    if (!miss.matched) {
      assert.equal(miss.reason, "no-entry");
    }

    cache.delete(`${APP}/private`);
    basicPut(cache, {
      request: { url: `${CDN}/private`, mode: "cors" },
      responseUrl: `${APP}/private`,
      type: "basic",
      credentialsMode: "omit",
    });
    const cross = cache.inspect({
      url: `${CDN}/private`,
      mode: "cors",
      credentials: "omit",
    });
    assert.equal(cross.matched, false);
    if (!cross.matched) {
      assert.equal(cross.reason, "response-type");
    }
  });

  it("caches opaque no-cors responses but never serves them as verified content", () => {
    const cache = new ResponseCache({ version: "v1" });
    const request: StructuredRequest = {
      url: `${CDN}/pixel`,
      mode: "no-cors",
      credentials: "omit",
    };

    const stored = cache.put({
      request,
      status: 0,
      type: "opaque",
      credentialsMode: "omit",
    });

    assert.equal(stored.origin, OPAQUE_ORIGIN);
    assert.equal(stored.status, 0);
    assert.equal(stored.body, undefined);
    assert.deepEqual(stored.headers, {});
    assert.equal(stored.integrityState, "unchecked");

    assert.ok(cache.match(request));

    const corsAttempt = cache.inspect({
      url: `${CDN}/pixel`,
      mode: "cors",
      credentials: "omit",
    });
    assert.equal(corsAttempt.matched, false);
    if (!corsAttempt.matched) {
      assert.equal(corsAttempt.reason, "opaque-for-cross-origin");
    }

    const integrityAttempt = cache.inspect({
      ...request,
      requireIntegrity: true,
    });
    assert.equal(integrityAttempt.matched, false);
    if (!integrityAttempt.matched) {
      assert.equal(integrityAttempt.reason, "integrity");
    }
  });

  it("rejects opaque responses that try to smuggle headers or a body", () => {
    const cache = new ResponseCache({ version: "v1" });
    assert.throws(
      () =>
        cache.put({
          request: { url: `${CDN}/pixel`, mode: "no-cors" },
          type: "opaque",
          credentialsMode: "omit",
          headers: { "set-cookie": "sid=1" },
        }),
      /opaque/,
    );
    assert.throws(
      () =>
        cache.put({
          request: { url: `${CDN}/pixel`, mode: "no-cors" },
          type: "opaque",
          credentialsMode: "omit",
          body: "secret",
        }),
      /opaque/,
    );
  });

  it("handles manual redirects with opaqueredirect entries only", () => {
    const cache = new ResponseCache({ version: "v1" });
    const manual: StructuredRequest = {
      url: `${APP}/go`,
      redirect: "manual",
      credentials: "omit",
    };
    const redirect = cache.put({
      request: manual,
      type: "opaqueredirect",
      credentialsMode: "omit",
      status: 0,
    });
    assert.equal(redirect.origin, OPAQUE_ORIGIN);
    assert.ok(cache.match(manual));

    const followAttempt = cache.inspect({ url: `${APP}/go`, redirect: "follow" });
    assert.equal(followAttempt.matched, false);
    if (!followAttempt.matched) {
      assert.equal(followAttempt.reason, "response-type");
    }
  });
});

describe("anonymous vs credentialed requests", () => {
  it("isolates omit, same-origin and include credential modes", () => {
    const cache = new ResponseCache({ version: "v1" });

    basicPut(cache, {
      request: { url: `${APP}/api/me`, credentials: "include" },
      credentialsMode: "include",
      body: "private",
    });

    const anonymous = cache.inspect({
      url: `${APP}/api/me`,
      credentials: "omit",
    });
    assert.equal(anonymous.matched, false);
    if (!anonymous.matched) {
      assert.equal(anonymous.reason, "credentials-mode");
    }

    const sameOriginRead = cache.inspect({
      url: `${APP}/api/me`,
      credentials: "same-origin",
    });
    assert.equal(sameOriginRead.matched, false);

    assert.equal(
      cache.match<string>({ url: `${APP}/api/me`, credentials: "include" })?.body,
      "private",
    );
  });

  it("allows same-origin reads to reuse anonymous entries but not the reverse", () => {
    const cache = new ResponseCache({ version: "v1" });
    basicPut(cache, {
      request: { url: `${APP}/public.js`, credentials: "omit" },
      credentialsMode: "omit",
    });

    assert.ok(cache.match({ url: `${APP}/public.js`, credentials: "same-origin" }));
    assert.ok(cache.match({ url: `${APP}/public.js`, credentials: "omit" }));

    const includeAttempt = cache.inspect({
      url: `${APP}/public.js`,
      credentials: "include",
    });
    assert.equal(includeAttempt.matched, false);
    if (!includeAttempt.matched) {
      assert.equal(includeAttempt.reason, "credentials-mode");
    }
  });

  it("keeps one entry per URL and never downgrades credential security on rewrite", () => {
    const cache = new ResponseCache({ version: "v1" });
    basicPut(cache, {
      request: { url: `${APP}/doc`, credentials: "omit" },
      credentialsMode: "omit",
      body: "anonymous-doc",
    });

    basicPut(cache, {
      request: { url: `${APP}/doc`, credentials: "include" },
      credentialsMode: "include",
      body: "personal-doc",
    });

    assert.equal(cache.size, 1);
    assert.equal(
      cache.match<string>({ url: `${APP}/doc`, credentials: "omit" }),
      undefined,
    );
    assert.equal(
      cache.match<string>({ url: `${APP}/doc`, credentials: "include" })?.body,
      "personal-doc",
    );
  });
});

describe("integrity state", () => {
  it("normalizes missing integrity metadata to unchecked, never verified", () => {
    const cache = new ResponseCache({ version: "v1" });
    const stored = basicPut(cache, {
      request: { url: `${APP}/no-sri.js` },
      integrityState: undefined,
      integrityMetadata: undefined,
    });
    assert.equal(stored.integrityState, "unchecked");
    assert.deepEqual(stored.integrityMetadata, []);

    const claimed = basicPut(cache, {
      request: { url: `${APP}/claim.js` },
      integrityState: "verified",
      integrityMetadata: [],
    });
    assert.equal(claimed.integrityState, "unchecked");

    const guarded = cache.inspect({
      url: `${APP}/claim.js`,
      requireIntegrity: true,
    });
    assert.equal(guarded.matched, false);
    if (!guarded.matched) {
      assert.equal(guarded.reason, "integrity");
    }
  });

  it("serves verified entries for integrity-guarded requests", () => {
    const cache = new ResponseCache({ version: "v1" });
    basicPut(cache, {
      request: { url: `${APP}/sri.js`, credentials: "omit" },
      credentialsMode: "omit",
      integrityState: "verified",
      integrityMetadata: [SRI],
    });

    const hit = cache.match({
      url: `${APP}/sri.js`,
      credentials: "omit",
      requireIntegrity: true,
    });
    assert.ok(hit);
    assert.equal(hit?.integrityState, "verified");
  });

  it("never serves mismatch entries and rejects malformed metadata", () => {
    const cache = new ResponseCache({ version: "v1" });
    basicPut(cache, {
      request: { url: `${APP}/bad.js` },
      integrityState: "mismatch",
      integrityMetadata: [SRI],
    });

    assert.equal(cache.match({ url: `${APP}/bad.js` }), undefined);
    const result = cache.inspect({ url: `${APP}/bad.js` });
    assert.equal(result.matched, false);
    if (!result.matched) {
      assert.equal(result.reason, "integrity");
    }

    assert.throws(
      () =>
        basicPut(cache, {
          request: { url: `${APP}/malformed.js` },
          integrityState: "verified",
          integrityMetadata: [{ algorithm: "md5" as never, digest: "xx" }],
        }),
      /malformed/,
    );
  });
});

describe("version switching", () => {
  it("stops matching entries from the previous cache version", () => {
    const v1 = new ResponseCache({ version: "v1" });
    basicPut(v1, { request: { url: `${APP}/app.js` }, body: "old" });

    const v2 = v1.switchVersion("v2");
    assert.equal(v2.version, "v2");
    assert.equal(v2.size, 1, "shared storage still holds the old entry");

    const stale = v2.inspect({ url: `${APP}/app.js` });
    assert.equal(stale.matched, false);
    if (!stale.matched) {
      assert.equal(stale.reason, "version");
    }

    basicPut(v2, {
      request: { url: `${APP}/app.js`, credentials: "omit" },
      credentialsMode: "omit",
      integrityState: "verified",
      integrityMetadata: [SRI],
      body: "new",
    });
    const hit = v2.match<string>({
      url: `${APP}/app.js`,
      credentials: "omit",
      requireIntegrity: true,
    });
    assert.equal(hit?.body, "new");
    assert.equal(hit?.cacheVersion, "v2");
  });

  it("isolates version views while sharing the backing storage", () => {
    const v1 = new ResponseCache({ version: "v1" });
    const v2 = v1.switchVersion("v2");

    basicPut(v1, { request: { url: `${APP}/shared.js` }, body: "v1-body" });
    assert.equal(v2.size, 1);
    assert.equal(v2.match({ url: `${APP}/shared.js` }), undefined);

    basicPut(v2, {
      request: { url: `${APP}/shared.js`, credentials: "omit" },
      credentialsMode: "omit",
      body: "v2-body",
    });
    assert.equal(v1.size, 1, "same key, overwritten in shared storage");
    assert.equal(
      v1.match<string>({ url: `${APP}/shared.js` }),
      undefined,
      "v1 view rejects the v2-stamped replacement",
    );
    assert.equal(
      v2.match<string>({ url: `${APP}/shared.js`, credentials: "omit" })?.body,
      "v2-body",
    );
  });

  it("requires a non-empty version", () => {
    assert.throws(() => new ResponseCache({ version: "" }), TypeError);
    const cache = new ResponseCache({ version: "v1" });
    assert.throws(() => cache.switchVersion(""), TypeError);
  });
});

describe("cache write guard rails", () => {
  it("rejects error responses and non-opaque no-cors responses", () => {
    const cache = new ResponseCache({ version: "v1" });
    assert.throws(
      () =>
        cache.put({
          request: { url: `${APP}/boom` },
          type: "error",
          credentialsMode: "omit",
          status: 0,
        }),
      /not cacheable/,
    );
    assert.throws(
      () =>
        cache.put({
          request: { url: `${CDN}/x`, mode: "no-cors" },
          type: "cors",
          credentialsMode: "omit",
          status: 200,
        }),
      /no-cors/,
    );
  });

  it("rejects malformed request URLs", () => {
    const cache = new ResponseCache({ version: "v1" });
    assert.throws(
      () =>
        cache.put({
          request: { url: "relative/path" },
          type: "basic",
          credentialsMode: "same-origin",
          status: 200,
        }),
      TypeError,
    );
  });
});

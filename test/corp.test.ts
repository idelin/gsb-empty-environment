import assert from "node:assert/strict";
import test from "node:test";

import {
  applyCorpPolicy,
  assertCorpAllowed,
  checkCorp,
  CorpBlockedError,
  evaluateCorp,
  filterCorpResponse,
  getCorpPolicyFromHeaders,
  getRegistrableDomain,
  isSameOrigin,
  isSameSite,
  parseCorpHeader,
  readCorpResponseBody,
} from "../index.ts";

test("parses only exact CORP tokens and treats malformed values as no policy", () => {
  assert.equal(parseCorpHeader("same-origin"), "same-origin");
  assert.equal(parseCorpHeader("same-site"), "same-site");
  assert.equal(parseCorpHeader("cross-origin"), "cross-origin");
  assert.equal(parseCorpHeader(" \tsame-site\t "), "same-site");

  assert.equal(parseCorpHeader(null), null);
  assert.equal(parseCorpHeader(undefined), null);
  assert.equal(parseCorpHeader(""), null);
  assert.equal(parseCorpHeader("none"), null);
  assert.equal(parseCorpHeader("same-origin, same-origin"), null);
  assert.equal(parseCorpHeader(["same-origin", "same-origin"]), null);
  assert.equal(parseCorpHeader("SAME-ORIGIN"), null);
});

test("reads the CORP header case-insensitively from header bags", () => {
  assert.equal(
    getCorpPolicyFromHeaders({
      "Cross-Origin-Resource-Policy": "same-site",
    }),
    "same-site",
  );
  assert.equal(
    getCorpPolicyFromHeaders(new Headers({
      "cross-origin-resource-policy": "same-origin",
    })),
    "same-origin",
  );
  assert.equal(getCorpPolicyFromHeaders({ "content-type": "image/png" }), null);
});

test("same-origin compares scheme, host, and effective port exactly", () => {
  assert.equal(isSameOrigin("https://example.com/a", "https://example.com/b"), true);
  assert.equal(isSameOrigin("https://example.com", "https://example.com:443"), true);
  assert.equal(isSameOrigin("https://example.com", "https://example.com:8443"), false);
  assert.equal(isSameOrigin("http://example.com", "https://example.com"), false);
  assert.equal(isSameOrigin("https://www.example.com", "https://example.com"), false);
  assert.equal(isSameOrigin("https://example.com.", "https://example.com"), true);
});

test("same-site uses PSL registrable domains and scheme, ignoring ports", () => {
  assert.equal(isSameSite("https://www.example.com", "https://api.example.com"), true);
  assert.equal(isSameSite("https://example.com:8443", "https://example.com:9443"), true);
  assert.equal(isSameSite("http://example.com", "https://example.com"), false);
  assert.equal(isSameSite("https://www.example.co.uk", "https://shop.example.co.uk"), true);
  assert.equal(isSameSite("https://a.example.co.uk", "https://b.other.co.uk"), false);

  // github.io is in the PSL private section. Different tenants are different
  // registrable domains even though both look like github.io subdomains.
  assert.equal(isSameSite("https://victim.github.io", "https://evil.github.io"), false);
  assert.equal(
    isSameSite("https://www.victim.github.io", "https://api.victim.github.io"),
    true,
  );
  assert.equal(
    isSameSite("https://victim.github.io", "https://www.victim.github.io"),
    true,
  );
});

test("computes registrable domains from wildcard and exception rules", () => {
  assert.equal(getRegistrableDomain("www.example.co.uk"), "example.co.uk");
  assert.equal(getRegistrableDomain("a.www.ck"), "www.ck");
  assert.equal(getRegistrableDomain("foo.bar.ck"), "foo.bar.ck");
  assert.equal(getRegistrableDomain("www.ck"), "www.ck");
  assert.equal(getRegistrableDomain("victim.github.io"), "victim.github.io");
  assert.equal(getRegistrableDomain("github.io"), null);
  assert.equal(getRegistrableDomain("127.0.0.1"), null);
});

test("same-origin policy blocks cross-origin reads", () => {
  const decision = evaluateCorp({
    initiatorOrigin: "https://evil.example",
    resourceUrl: "https://images.example/photo.png",
    corpHeader: "same-origin",
  });

  assert.equal(decision.policy, "same-origin");
  assert.equal(decision.blocked, true);
  assert.equal(decision.reason, "origin-mismatch");
  assert.equal(decision.sameOrigin, false);
});

test("same-site cannot be bypassed through another github.io tenant", () => {
  const decision = checkCorp(
    "https://attacker.github.io",
    "https://victim.github.io/private.png",
    "same-site",
  );

  assert.equal(decision.blocked, true);
  assert.equal(decision.reason, "site-mismatch");
  assert.equal(decision.initiatorSite, "attacker.github.io");
  assert.equal(decision.resourceSite, "victim.github.io");
});

test("same-site allows another port but rejects an HTTP to HTTPS upgrade", () => {
  assert.equal(
    evaluateCorp({
      initiatorOrigin: "https://example.com:8443",
      resourceUrl: "https://example.com:9443/image",
      corpHeader: "same-site",
    }).allowed,
    true,
  );

  const upgraded = evaluateCorp({
    initiatorOrigin: "http://example.com",
    resourceUrl: "https://example.com/image",
    corpHeader: "same-site",
  });
  assert.equal(upgraded.blocked, true);
  assert.equal(upgraded.sameSite, false);
});

test("missing or invalid policy does not block, cross-origin explicitly allows", () => {
  assert.equal(evaluateCorp({
    initiatorOrigin: "https://attacker.github.io",
    resourceUrl: "https://victim.github.io/image",
    corpHeader: "not-a-policy",
  }).allowed, true);

  assert.equal(evaluateCorp({
    initiatorOrigin: "https://attacker.github.io",
    resourceUrl: "https://victim.github.io/image",
  }).allowed, true);

  assert.equal(evaluateCorp({
    initiatorOrigin: "https://attacker.github.io",
    resourceUrl: "https://victim.github.io/image",
    corpHeader: "cross-origin",
  }).allowed, true);
});

test("blocked bodies are not present in guarded results", () => {
  const result = applyCorpPolicy({
    initiatorOrigin: "https://evil.github.io",
    resourceUrl: "https://victim.github.io/private.png",
    headers: { "cross-origin-resource-policy": "same-site" },
    body: "protected bytes",
  });

  assert.equal(result.blocked, true);
  assert.equal("body" in result, false);
  assert.deepEqual(result.body, undefined);

  assert.throws(
    () => assertCorpAllowed({
      initiatorOrigin: "https://evil.github.io",
      resourceUrl: "https://victim.github.io/private.png",
      corpHeader: "same-site",
    }),
    CorpBlockedError,
  );

  const allowed = applyCorpPolicy({
    initiatorOrigin: "https://www.victim.github.io",
    resourceUrl: "https://api.victim.github.io/image",
    corpHeader: "same-site",
    body: "image bytes",
  });
  assert.equal(allowed.body, "image bytes");
});

test("Fetch response bodies are cancelled and never read when blocked", async () => {
  const resourceUrl = "https://victim.github.io/private.png";
  const initiatorOrigin = "https://evil.github.io";
  const response = new Response("protected bytes", {
    headers: { "cross-origin-resource-policy": "same-site" },
  });

  assert.equal(
    await readCorpResponseBody(response, { resourceUrl, initiatorOrigin }),
    null,
  );
  assert.equal(response.bodyUsed, true);

  const allowedResponse = new Response("image bytes", {
    headers: { "cross-origin-resource-policy": "same-site" },
  });
  const body = await readCorpResponseBody(allowedResponse, {
    resourceUrl: "https://api.victim.github.io/image",
    initiatorOrigin: "https://www.victim.github.io",
  });
  assert.ok(body instanceof ArrayBuffer);

  const decision = await filterCorpResponse(
    new Response("protected bytes", {
      headers: { "cross-origin-resource-policy": "same-origin" },
    }),
    {
      resourceUrl,
      initiatorOrigin,
    },
  );
  assert.equal(decision.blocked, true);
  assert.equal(decision.body, null);
  assert.equal(decision.response, null);
});

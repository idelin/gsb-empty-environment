import test from "node:test";
import assert from "node:assert/strict";
import {
  OneTimeRelay,
  createRedirectHandler,
  createRedirectResponse,
  createReferrerPortal,
  handleRedirectRequest,
  sanitizeRedirectTarget,
  stripRedirectQuery,
} from "../index.ts";

function createNodeResponse() {
  const headers: Record<string, string> = {};
  return {
    statusCode: 200,
    body: "",
    headers,
    setHeader(name: string, value: string) {
      headers[name] = value;
    },
    end(body = "") {
      this.body = body;
    },
  };
}

test("redirect handler strips tokens server-side and never copies request query", () => {
  const result = handleRedirectRequest(
    "/__referrer-redirect?url=https%3A%2F%2Fthird.example%2Fcallback%3Freset_token%3Dabc%26topic%3Dsafe&token=request-secret",
    { mountPath: "/__referrer-redirect" },
  );

  assert.equal(result.status, 302);
  assert.equal(result.location, "https://third.example/callback");
  assert.doesNotMatch(result.location ?? "", /abc|request-secret|token/);
  assert.equal(result.headers["Referrer-Policy"], "no-referrer");
  assert.equal(result.headers["Cache-Control"], "no-store");
});

test("cross-origin redirects default to a one-time relay when configured", () => {
  const relay = createRelayForTest();
  const result = handleRedirectRequest(
    "/__referrer-redirect?url=https%3A%2F%2Fthird.example%2Fcallback%3Ftoken%3Dabc%26topic%3Dhelp",
    {
      mountPath: "/__referrer-redirect",
      relay,
      externalMode: "relay",
    },
  );

  assert.equal(result.status, 302);
  assert.match(result.location ?? "", /\/relay\?t=/);
  assert.doesNotMatch(
    result.location ?? "",
    /third\.example|reset_token|topic=/,
  );
  assert.ok(result.relayToken);
});

test("legitimate external redirect keeps only non-sensitive parameters", () => {
  const result = handleRedirectRequest(
    "/__referrer-redirect?target=https%3A%2F%2Fpartner.example%2Flanding%3Futm_source%3Dportal",
    { mountPath: "/__referrer-redirect", externalMode: "direct" },
  );

  assert.equal(result.status, 302);
  assert.equal(
    result.location,
    "https://partner.example/landing?utm_source=portal",
  );
});

test("a redirect containing a token drops its entire query", () => {
  const result = handleRedirectRequest(
    "/__referrer-redirect?target=https%3A%2F%2Fpartner.example%2Flanding%3Futm_source%3Dportal%26reset_token%3Dabc",
    { mountPath: "/__referrer-redirect", externalMode: "direct" },
  );

  assert.equal(result.status, 302);
  assert.equal(result.location, "https://partner.example/landing");
});

test("relative redirects stay relative and cannot be turned into open redirects by Host", () => {
  const result = handleRedirectRequest(
    "/redirect?url=%2Faccount%3Freset_token%3Dabc",
    { mountPath: "/redirect", baseUrl: "https://portal.example" },
  );

  assert.equal(result.status, 302);
  assert.equal(result.location, "/account");
});

test("redirect origin allow-list blocks non-listed destinations", () => {
  const blocked = handleRedirectRequest(
    "/redirect?url=https%3A%2F%2Fevil.example%2Fcb",
    {
      mountPath: "/redirect",
      allowedOrigins: ["https://partner.example"],
      allowAnyExternal: false,
    },
  );
  assert.equal(blocked.status, 403);
  assert.equal(blocked.headers.Location, undefined);

  const allowed = handleRedirectRequest(
    "/redirect?url=https%3A%2F%2Fpartner.example%2Fcb",
    {
      mountPath: "/redirect",
      allowedOrigins: ["https://partner.example"],
      allowAnyExternal: false,
    },
  );
  assert.equal(allowed.status, 302);
  assert.equal(allowed.location, "https://partner.example/cb");
});

test("redirect target sanitizer rejects credentials and unsafe schemes", () => {
  assert.equal(
    sanitizeRedirectTarget("https://user:password@third.example/"),
    null,
  );
  assert.equal(
    sanitizeRedirectTarget("javascript:alert(1)"),
    null,
  );
  assert.equal(
    sanitizeRedirectTarget("data:text/html,<script>alert(1)</script>"),
    null,
  );
});

test("the redirect endpoint strips the whole query when configured", () => {
  assert.equal(
    stripRedirectQuery("https://third.example/cb?token=abc&topic=help"),
    "https://third.example/cb",
  );

  const result = handleRedirectRequest(
    "/__referrer-redirect?url=https%3A%2F%2Fthird.example%2Fcb%3Ftopic%3Dhelp",
    { mountPath: "/__referrer-redirect", stripAllQuery: true },
  );
  assert.equal(result.location, "https://third.example/cb");
});

test("Node redirect handler writes Location and strict headers", () => {
  const handler = createRedirectHandler();
  const req = {
    url: "/__referrer-redirect?url=https%3A%2F%2Fthird.example%2Fcb%3Ftoken%3Dabc",
    method: "GET",
  };
  const res = createNodeResponse();

  assert.equal(handler(req, res), true);
  assert.equal(res.statusCode, 302);
  assert.equal(res.headers.Location, "https://third.example/cb");
  assert.equal(res.headers["Referrer-Policy"], "no-referrer");
});

test("Node redirect handler recognizes the conventional /redirect alias", () => {
  const handler = createRedirectHandler();
  const req = {
    url: "/redirect?url=https%3A%2F%2Fthird.example%2Fcb%3Ftoken%3Dabc",
    method: "GET",
  };
  const res = createNodeResponse();

  assert.equal(handler(req, res), true);
  assert.equal(res.headers.Location, "https://third.example/cb");
});

test("portal redirect defaults to a direct sanitized Location", () => {
  const portal = createReferrerPortal({ baseUrl: "https://portal.example" });
  const result = portal.redirect(
    "/__referrer-redirect?url=https%3A%2F%2Fthird.example%2Fcb%3Freset_token%3Dabc%26topic%3Dhelp",
  );

  assert.equal(result.status, 302);
  assert.equal(result.location, "https://third.example/cb");
  assert.equal(result.headers["Referrer-Policy"], "no-referrer");
});

test("redirect result can be adapted to a fetch Response", () => {
  const response = createRedirectResponse(
    "/redirect?url=https%3A%2F%2Fthird.example%2Fcb%3Ftoken%3Dabc",
  ) as Response;

  assert.equal(response.status, 302);
  assert.equal(response.headers.get("Location"), "https://third.example/cb");
  assert.equal(response.headers.get("Referrer-Policy"), "no-referrer");
});

function createRelayForTest(): OneTimeRelay {
  return new OneTimeRelay({
    baseUrl: "https://portal.example",
    mountPath: "/relay",
  });
}

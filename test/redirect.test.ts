import assert from "node:assert/strict";
import test from "node:test";

import {
  RedirectError,
  assertSafeRedirectTarget,
  buildRedirectLocation,
  createLoginRedirectHandler,
  createRedirectResponse,
  sendRedirect,
  validateHeaders,
  validateRedirectResponse,
} from "../index.ts";

const BASE = "https://login.example.com/";
const ALLOWED = "https://account.example.com";

function locationFor(next: string): string {
  return createRedirectResponse(next, {
    baseUrl: BASE,
    allowlist: [ALLOWED],
  }).headers.Location;
}

test("builds a normalized same-site relative redirect", () => {
  assert.equal(locationFor("/dashboard"), "/dashboard");
  assert.equal(locationFor("/a/./b/../c"), "/a/c");
  assert.equal(locationFor("/%7e/./profile"), "/~/profile");
  assert.equal(locationFor("/a/%2e%2e/b"), "/b");
  assert.equal(
    locationFor("/account/settings?tab=%7e#%41"),
    "/account/settings?tab=~#A",
  );
  assert.equal(locationFor("dashboard"), "/dashboard");
});

test("allows only allowlisted or same-origin absolute URLs", () => {
  assert.equal(
    locationFor("HTTPS://ACCOUNT.EXAMPLE.COM:443/%7Euser"),
    "https://account.example.com/~user",
  );
  assert.equal(
    locationFor("https://login.example.com/account"),
    "https://login.example.com/account",
  );
});

test("rejects off-allowlist and protocol-relative targets", () => {
  assert.equal(locationFor("https://evil.example/steal"), "/");
  assert.equal(locationFor("https://account.example.com.evil.example/"), "/");
  assert.equal(locationFor("https://account.example.com@evil.example/"), "/");
  assert.equal(locationFor("http://account.example.com/"), "/");
  assert.equal(locationFor("//evil.example/"), "/");
  assert.equal(locationFor("///evil.example/"), "/");
  assert.equal(locationFor("/%2f%2fevil.example/"), "/");
  assert.equal(locationFor("%2f%2fevil.example/"), "/");
  assert.equal(locationFor("https:%2f%2fevil.example/"), "/");
});

test("preserves reserved encoded slashes that stay same-site", () => {
  assert.equal(
    locationFor("/files%2Freport?url=https%3A%2F%2Fexample.com%2F"),
    "/files%2Freport?url=https%3A%2F%2Fexample.com%2F",
  );
});

test("rejects CRLF and encoded CRLF injection", () => {
  assert.equal(locationFor("/ok\r\nSet-Cookie: pwned=1"), "/");
  assert.equal(locationFor("/ok%0d%0aSet-Cookie:%20pwned=1"), "/");
  assert.equal(locationFor("/ok%0D%0ASet-Cookie:%20pwned=1"), "/");
  assert.equal(locationFor("/ok\u000bSet-Cookie: pwned=1"), "/");
});

test("rejects double and repeated encoding", () => {
  assert.equal(locationFor("/%250d%250aX-Injected:%201"), "/");
  assert.equal(locationFor("/%252f%252fevil.example"), "/");
  assert.equal(locationFor("/%252e%252e/admin"), "/");
  assert.equal(locationFor("/%25250d%25250aX:%201"), "/");
  assert.equal(locationFor("/%25E4%25B8%25AD"), "/");
});

test("rejects Unicode line separators and backslashes", () => {
  assert.equal(locationFor("/ok\u2028Set-Cookie: pwned=1"), "/");
  assert.equal(locationFor("/ok\u2029Set-Cookie: pwned=1"), "/");
  assert.equal(locationFor("/ok\u0085Set-Cookie: pwned=1"), "/");
  assert.equal(locationFor("/ok\\evil.example"), "/");
  assert.equal(locationFor("/ok%5cevil.example"), "/");
});

test("rejects bare percent signs and malformed escapes", () => {
  assert.equal(locationFor("/100%"), "/");
  assert.equal(locationFor("/100%2"), "/");
  assert.equal(locationFor("/100%zz"), "/");
  assert.equal(locationFor("/%FF"), "/");
});

test("rejects non-http schemes", () => {
  assert.equal(locationFor("javascript:alert(1)"), "/");
  assert.equal(locationFor("data:text/html,<script>alert(1)</script>"), "/");
  assert.equal(locationFor("mailto:user@example.com"), "/");
});

test("uses a validated configured fallback", () => {
  const response = createRedirectResponse("//evil.example/", {
    baseUrl: BASE,
    fallback: "/login",
  });
  assert.equal(response.headers.Location, "/login");

  assert.throws(
    () =>
      createRedirectResponse("/", {
        baseUrl: BASE,
        fallback: "//evil.example/",
      }),
    (error: unknown) =>
      error instanceof RedirectError && error.code === "INVALID_OPTIONS",
  );
});

test("supports an absolute URL path allowlist without traversal", () => {
  assert.equal(
    createRedirectResponse("https://account.example.com/callback/step-2", {
      baseUrl: BASE,
      allowlist: ["https://account.example.com/callback"],
    }).headers.Location,
    "https://account.example.com/callback/step-2",
  );
  assert.equal(
    createRedirectResponse(
      "https://account.example.com/callback/../private",
      {
        baseUrl: BASE,
        allowlist: ["https://account.example.com/callback"],
      },
    ).headers.Location,
    "/",
  );
  assert.equal(
    createRedirectResponse(
      "https://account.example.com/callback%2F..%2Fprivate",
      {
        baseUrl: BASE,
        allowlist: ["https://account.example.com/callback"],
      },
    ).headers.Location,
    "/",
  );
});

test("one authoritative builder creates exactly one Location", () => {
  const response = createRedirectResponse("/dashboard", {
    baseUrl: BASE,
    headers: { "X-Request-Id": "abc-123" },
  });

  const locations = Object.keys(response.headers).filter(
    (name) => name.toLowerCase() === "location",
  );
  assert.equal(locations.length, 1);
  assert.equal(response.headers.Location, "/dashboard");
  assert.equal(response.headers["X-Request-Id"], "abc-123");
  assert.equal(response.status, 302);
  assert.equal(response.statusCode, 302);
  assert.equal(response.body, "");
  assert.equal(response.headers["Content-Length"], "0");
});

test("rejects caller-supplied Location headers", () => {
  assert.throws(
    () =>
      createRedirectResponse("/dashboard", {
        baseUrl: BASE,
        headers: { Location: "/evil" },
      }),
    (error: unknown) =>
      error instanceof RedirectError && error.code === "DUPLICATE_LOCATION",
  );

  assert.throws(
    () =>
      createRedirectResponse("/dashboard", {
        baseUrl: BASE,
        headers: [
          ["Location", "/one"],
          ["location", "/two"],
        ],
      }),
    (error: unknown) =>
      error instanceof RedirectError && error.code === "DUPLICATE_LOCATION",
  );
});

test("validates header names and values before sending", () => {
  assert.throws(
    () =>
      validateHeaders({
        ["X-Bad\r\nSet-Cookie"]: "value",
      }),
    (error: unknown) =>
      error instanceof RedirectError && error.code === "INVALID_HEADER_NAME",
  );
  assert.throws(
    () =>
      validateHeaders({
        ["X-Bad"]: "value\r\nSet-Cookie: pwned=1",
      }),
    (error: unknown) =>
      error instanceof RedirectError && error.code === "INVALID_HEADER_VALUE",
  );
  assert.throws(
    () =>
      validateHeaders({
        ["X-Bad"]: "value\u2028Set-Cookie: pwned=1",
      }),
    (error: unknown) =>
      error instanceof RedirectError && error.code === "INVALID_HEADER_VALUE",
  );
});

test("response body and status stay consistent", () => {
  const response = createRedirectResponse("/dashboard", {
    baseUrl: BASE,
    status: 303,
    body: "Redirecting",
  });
  assert.equal(response.status, 303);
  assert.equal(response.headers.Location, "/dashboard");
  assert.equal(response.headers["Content-Length"], "11");
  assert.equal(response.headers["Content-Type"], "text/plain; charset=utf-8");
  validateRedirectResponse(response);

  assert.throws(
    () =>
      createRedirectResponse("/dashboard", {
        baseUrl: BASE,
        body: "abc",
        headers: { "Content-Length": "4" },
      }),
    (error: unknown) =>
      error instanceof RedirectError && error.code === "INVALID_HEADER_VALUE",
  );

  assert.throws(
    () =>
      createRedirectResponse("/dashboard", {
        baseUrl: BASE,
        headers: { "Transfer-Encoding": "chunked" },
      }),
    (error: unknown) =>
      error instanceof RedirectError && error.code === "INVALID_HEADER_VALUE",
  );

  assert.throws(
    () =>
      createRedirectResponse("/dashboard", {
        baseUrl: BASE,
        status: 200 as never,
      }),
    (error: unknown) =>
      error instanceof RedirectError && error.code === "INVALID_STATUS",
  );
});

class MockResponse {
  statusCode = 200;
  statusMessage = "OK";
  body = "";
  readonly headerValues = new Map<string, { name: string; value: string }>();

  setHeader(name: string, value: string): void {
    this.headerValues.set(name.toLowerCase(), { name, value });
  }

  appendHeader(name: string, value: string): void {
    const key = name.toLowerCase();
    const existing = this.headerValues.get(key);
    if (existing === undefined) {
      this.headerValues.set(key, { name, value });
    } else {
      this.headerValues.set(key, {
        name: existing.name,
        value: `${existing.value}, ${value}`,
      });
    }
  }

  removeHeader(name: string): void {
    this.headerValues.delete(name.toLowerCase());
  }

  getHeaderNames(): string[] {
    return [...this.headerValues.values()].map((entry) => entry.name);
  }

  end(body: string): void {
    this.body = body;
  }
}

test("send adapter removes a pre-existing Location and writes one", () => {
  const response = new MockResponse();
  response.appendHeader("Location", "https://evil.example/");
  response.appendHeader("location", "https://evil.example/second");

  const redirect = createRedirectResponse("/dashboard", { baseUrl: BASE });
  sendRedirect(response, redirect);

  const locations = response.getHeaderNames().filter(
    (name) => name.toLowerCase() === "location",
  );
  assert.deepEqual(locations, ["Location"]);
  assert.equal(response.headerValues.get("location")?.value, "/dashboard");
  assert.equal(response.statusCode, 302);
  assert.equal(response.statusMessage, "Found");
  assert.equal(response.body, "");
});

test("send adapter accepts either argument order and shorthand targets", () => {
  const first = new MockResponse();
  const redirect = createRedirectResponse("/account", { baseUrl: BASE });
  sendRedirect(redirect, first);
  assert.equal(first.headerValues.get("location")?.value, "/account");

  const second = new MockResponse();
  sendRedirect(second, "/settings", { baseUrl: BASE });
  assert.equal(second.headerValues.get("location")?.value, "/settings");
});

test("login handler validates the decoded next parameter", () => {
  const handler = createLoginRedirectHandler({
    baseUrl: BASE,
    allowlist: [ALLOWED],
  });

  const safe = new MockResponse();
  handler(
    { url: "/login?next=%2Faccount%2Fsettings%3Ftab%3Dsecurity" },
    safe,
  );
  assert.equal(safe.headerValues.get("location")?.value, "/account/settings?tab=security");

  const injected = new MockResponse();
  handler(
    { url: "/login?next=%2Fok%250d%250aSet-Cookie%3A%2520pwned%3D1" },
    injected,
  );
  assert.equal(injected.headerValues.get("location")?.value, "/");
  assert.equal(injected.headerValues.has("set-cookie"), false);

  const protocolRelative = new MockResponse();
  handler(
    { url: "/login?next=%252f%252fevil.example%2F" },
    protocolRelative,
  );
  assert.equal(protocolRelative.headerValues.get("location")?.value, "/");

  const ambiguous = new MockResponse();
  handler(
    { url: "/login?next=%2Fsafe&next=%2Fother" },
    ambiguous,
  );
  assert.equal(ambiguous.headerValues.get("location")?.value, "/");

  const preparsed = new MockResponse();
  handler({ url: "/login", query: { next: "/account" } }, preparsed);
  assert.equal(preparsed.headerValues.get("location")?.value, "/account");
});

test("Location normalization is not altered by body or extra headers", () => {
  const response = createRedirectResponse("/account?x=1#section", {
    baseUrl: BASE,
    headers: {
      "Cache-Control": "no-store",
      "X-Content-Type-Options": "nosniff",
    },
    body: "Redirecting to your account.",
  });
  assert.equal(response.headers.Location, "/account?x=1#section");
  assert.equal(
    [...Object.keys(response.headers)]
      .filter((name) => name.toLowerCase() === "location")
      .length,
    1,
  );
  validateRedirectResponse(response);
});

test("send-time validation rejects a mutated Location", () => {
  const response = createRedirectResponse("/dashboard", { baseUrl: BASE });
  response.headers = {
    ...response.headers,
    Location: "/safe\r\nSet-Cookie: pwned=1",
  };

  assert.throws(
    () => validateRedirectResponse(response),
    (error: unknown) =>
      error instanceof RedirectError && error.code === "INVALID_HEADER_VALUE",
  );
});

test("strict target validation throws for rejected values", () => {
  assert.equal(
    assertSafeRedirectTarget("/dashboard", { baseUrl: BASE }),
    "/dashboard",
  );
  assert.throws(
    () => assertSafeRedirectTarget("//evil.example/", { baseUrl: BASE }),
    (error: unknown) =>
      error instanceof RedirectError && error.code === "INVALID_TARGET",
  );
});

test("accepts base URL strings and allowlist arrays as shorthand", () => {
  assert.equal(
    createRedirectResponse("/dashboard", BASE).headers.Location,
    "/dashboard",
  );
  assert.equal(
    createRedirectResponse("https://account.example.com/profile", [
      ALLOWED,
    ]).headers.Location,
    "https://account.example.com/profile",
  );
});

test("buildRedirectLocation is the single location API", () => {
  assert.equal(
    buildRedirectLocation("/dashboard", {
      baseUrl: BASE,
      allowlist: [ALLOWED],
    }),
    "/dashboard",
  );
});

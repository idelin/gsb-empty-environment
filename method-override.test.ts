import test from "node:test";
import assert from "node:assert/strict";

import {
  authorizeRequest,
  createMethodOverrideGuard,
  getEffectiveMethod,
  isTrustedOverrideSource,
  normalizeMethod,
  resolveRequestMethod,
} from "./index.ts";

const API_URL = "https://api.example.com/items/42";
const APP_ORIGIN = "https://app.example.com";
const EVIL_ORIGIN = "https://evil.example.com";

const trustedOptions = {
  enabled: true,
  trustedOrigins: [APP_ORIGIN],
  allowSameOrigin: false,
};

function post(overrides: {
  origin?: string;
  headers?: Record<string, string | string[]>;
  body?: unknown;
  url?: string;
  trusted?: boolean;
} = {}) {
  return {
    method: "POST",
    url: overrides.url ?? API_URL,
    origin: overrides.origin ?? APP_ORIGIN,
    headers: overrides.headers,
    body: overrides.body,
    trusted: overrides.trusted,
  };
}

function allowAll() {
  return true;
}

test("override is disabled by default and the header is stripped", () => {
  const resolution = resolveRequestMethod(
    post({
      headers: {
        "x-http-method-override": "DELETE",
        "content-type": "application/x-www-form-urlencoded",
        "x-request-id": "req-1",
      },
      body: "name=demo",
    }),
  );

  assert.equal(resolution.valid, true);
  assert.equal(resolution.originalMethod, "POST");
  assert.equal(resolution.effectiveMethod, "POST");
  assert.equal(resolution.overrideDeclared, true);
  assert.equal(resolution.overrideApplied, false);
  assert.equal(resolution.reasonCode, "OVERRIDE_DISABLED");
  assert.deepEqual(resolution.authorizationMethods, ["POST"]);
  assert.deepEqual(resolution.security, {
    csrf: { method: "POST" },
    audit: { method: "POST" },
    rateLimit: { method: "POST" },
  });
  assert.equal(resolution.forwardHeaders["x-http-method-override"], undefined);
  assert.equal(resolution.forwardHeaders["x-request-id"], "req-1");
});

test("disabled override can be failed closed instead of ignored", async () => {
  const decision = await authorizeRequest(
    post({ headers: { "x-http-method-override": "DELETE" } }),
    { unhonoredOverridePolicy: "reject" },
    () => {
      throw new Error("authorizer must not run for a rejected request");
    },
  );

  assert.equal(decision.valid, false);
  assert.equal(decision.allowed, false);
  assert.equal(decision.outcome, "deny");
  assert.equal(decision.reasonCode, "OVERRIDE_DISABLED");
  assert.equal(decision.authorization.evaluated, false);
});

test("trusted header override to DELETE authorizes both POST and DELETE", async () => {
  const calls: Array<{ method: string; phase: string; csrfMethod: string; auditMethod: string; rateLimitMethod: string }> = [];
  const decision = await authorizeRequest(
    post({ headers: { "X-HTTP-Method-Override": "DELETE" } }),
    trustedOptions,
    (context) => {
      calls.push({
        method: context.method,
        phase: context.phase,
        csrfMethod: context.csrfMethod,
        auditMethod: context.auditMethod,
        rateLimitMethod: context.rateLimitMethod,
      });
      return true;
    },
  );

  assert.equal(decision.valid, true);
  assert.equal(decision.originalMethod, "POST");
  assert.equal(decision.effectiveMethod, "DELETE");
  assert.equal(decision.overrideApplied, true);
  assert.equal(decision.overrideSource, "header");
  assert.deepEqual(decision.authorizationMethods, ["POST", "DELETE"]);
  assert.equal(decision.allowed, true);
  assert.equal(decision.outcome, "allow");
  assert.deepEqual(
    calls,
    [
      { method: "POST", phase: "original", csrfMethod: "POST", auditMethod: "POST", rateLimitMethod: "POST" },
      { method: "DELETE", phase: "effective", csrfMethod: "POST", auditMethod: "POST", rateLimitMethod: "POST" },
    ],
  );

  assert.equal(decision.forwardHeaders["x-http-method-override"], undefined);
});

test("the stricter authorization result wins and both methods are evaluated", async () => {
  const calls: string[] = [];
  const decision = await authorizeRequest(
    post({ headers: { "x-http-method-override": "DELETE" } }),
    trustedOptions,
    (context) => {
      calls.push(context.method);
      return context.method === "POST";
    },
  );

  assert.deepEqual(calls, ["POST", "DELETE"]);
  assert.equal(decision.allowed, false);
  assert.equal(decision.outcome, "deny");
  assert.deepEqual(decision.authorization.deniedBy, ["DELETE"]);
  assert.equal(decision.authorization.checks.length, 2);
  assert.equal(decision.authorization.checks[1].method, "DELETE");
  assert.equal(decision.authorization.checks[1].allowed, false);
});

test("a challenge from the effective method is stricter than allow", async () => {
  const decision = await authorizeRequest(
    post({ headers: { "x-http-method-override": "DELETE" } }),
    trustedOptions,
    (context) => (context.method === "DELETE" ? "challenge" : "allow"),
  );

  assert.equal(decision.allowed, false);
  assert.equal(decision.outcome, "challenge");
  assert.deepEqual(decision.authorization.challengedBy, ["DELETE"]);
});

test("conflicting double override headers are rejected before authorization", async () => {
  const calls: string[] = [];
  const decision = await authorizeRequest(
    post({
      headers: {
        "x-http-method-override": "DELETE",
        "x-method-override": "PATCH",
      },
    }),
    trustedOptions,
    (context) => {
      calls.push(context.method);
      return true;
    },
  );

  assert.equal(decision.valid, false);
  assert.equal(decision.rejected, true);
  assert.equal(decision.reasonCode, "CONFLICTING_HEADER_METHODS");
  assert.equal(decision.allowed, false);
  assert.deepEqual(calls, []);
});

test("repeated identical override headers are treated as one declaration", () => {
  const headers = new Map<string, string | string[]>([
    ["x-http-method-override", ["DELETE", "DELETE"]],
  ]);
  const resolution = resolveRequestMethod(post({ headers }), trustedOptions);

  assert.equal(resolution.valid, true);
  assert.equal(resolution.effectiveMethod, "DELETE");
  assert.equal(resolution.declarations.length, 2);
});

test("form _method override to DELETE is authorized as both POST and DELETE", async () => {
  const body = new URLSearchParams({ _method: "DELETE", id: "42" });
  const calls: string[] = [];
  const decision = await authorizeRequest(
    post({
      headers: { "content-type": "application/x-www-form-urlencoded" },
      body,
    }),
    trustedOptions,
    (context) => {
      calls.push(context.method);
      return true;
    },
  );

  assert.equal(decision.effectiveMethod, "DELETE");
  assert.equal(decision.overrideSource, "body");
  assert.deepEqual(calls, ["POST", "DELETE"]);
  assert.equal(decision.allowed, true);
  assert.ok(decision.forwardBody instanceof URLSearchParams);
  assert.equal((decision.forwardBody as URLSearchParams).get("_method"), null);
  assert.equal((decision.forwardBody as URLSearchParams).get("id"), "42");
});

test("string form body and object form body both expose _method", () => {
  const stringBody = resolveRequestMethod(
    post({
      headers: { "content-type": "application/x-www-form-urlencoded; charset=utf-8" },
      body: "_method=DELETE&id=42",
    }),
    trustedOptions,
  );
  assert.equal(stringBody.effectiveMethod, "DELETE");
  assert.equal(stringBody.forwardBody, "id=42");

  const objectBody = resolveRequestMethod(
    post({
      headers: { "content-type": "application/x-www-form-urlencoded" },
      body: { _method: "DELETE", id: "42" },
    }),
    trustedOptions,
  );
  assert.equal(objectBody.effectiveMethod, "DELETE");
  assert.deepEqual(objectBody.forwardBody, { id: "42" });
});

test("_method in an explicitly JSON body is not a form override", () => {
  const resolution = resolveRequestMethod(
    post({
      headers: { "content-type": "application/json" },
      body: { _method: "DELETE", id: "42" },
    }),
    trustedOptions,
  );

  assert.equal(resolution.overrideDeclared, false);
  assert.equal(resolution.effectiveMethod, "POST");
});

test("different methods in the header and body are rejected", async () => {
  const calls: string[] = [];
  const decision = await authorizeRequest(
    post({
      headers: {
        "content-type": "application/x-www-form-urlencoded",
        "x-http-method-override": "DELETE",
      },
      body: "_method=PATCH",
    }),
    trustedOptions,
    (context) => {
      calls.push(context.method);
      return true;
    },
  );

  assert.equal(decision.valid, false);
  assert.equal(decision.reasonCode, "CONFLICTING_BODY_AND_HEADER");
  assert.equal(decision.allowed, false);
  assert.deepEqual(calls, []);
});

test("matching header and body declarations are accepted", async () => {
  const decision = await authorizeRequest(
    post({
      headers: {
        "content-type": "application/x-www-form-urlencoded",
        "x-http-method-override": "DELETE",
      },
      body: "_method=DELETE",
    }),
    trustedOptions,
    allowAll,
  );

  assert.equal(decision.valid, true);
  assert.equal(decision.effectiveMethod, "DELETE");
  assert.equal(decision.overrideSource, "both");
  assert.deepEqual(decision.authorization.methods, ["POST", "DELETE"]);
  assert.equal(decision.allowed, true);
});

test("cross-origin override is not applied and POST still gets full authorization", async () => {
  const calls: string[] = [];
  const decision = await authorizeRequest(
    post({
      origin: EVIL_ORIGIN,
      headers: { "x-http-method-override": "DELETE" },
    }),
    trustedOptions,
    (context) => {
      calls.push(context.method);
      return context.method === "POST";
    },
  );

  assert.equal(decision.valid, true);
  assert.equal(decision.originalMethod, "POST");
  assert.equal(decision.effectiveMethod, "POST");
  assert.equal(decision.overrideApplied, false);
  assert.equal(decision.reasonCode, "UNTRUSTED_OVERRIDE");
  assert.deepEqual(calls, ["POST"]);
  assert.equal(decision.allowed, true);
  assert.equal(decision.forwardHeaders["x-http-method-override"], undefined);
});

test("cross-site fetch metadata cannot inherit same-origin trust", () => {
  const resolution = resolveRequestMethod(
    {
      method: "POST",
      url: API_URL,
      origin: API_URL,
      headers: {
        "sec-fetch-site": "cross-site",
        "x-http-method-override": "DELETE",
      },
    },
    { enabled: true },
  );

  assert.equal(resolution.effectiveMethod, "POST");
  assert.equal(resolution.reasonCode, "UNTRUSTED_OVERRIDE");
});

test("cross-origin override can be failed closed", async () => {
  const decision = await authorizeRequest(
    post({
      origin: EVIL_ORIGIN,
      headers: { "x-http-method-override": "DELETE" },
    }),
    { ...trustedOptions, unhonoredOverridePolicy: "reject" },
    allowAll,
  );

  assert.equal(decision.valid, false);
  assert.equal(decision.reasonCode, "UNTRUSTED_OVERRIDE");
  assert.equal(decision.allowed, false);
  assert.equal(decision.authorization.evaluated, false);
});

test("same-origin POST may use a legitimate override", async () => {
  const resolution = resolveRequestMethod(
    {
      method: "POST",
      url: API_URL,
      origin: API_URL,
      headers: { "x-http-method-override": "DELETE" },
    },
    { enabled: true },
  );

  assert.equal(isTrustedOverrideSource(
    { method: "POST", url: API_URL, origin: API_URL },
    { enabled: true },
  ), true);
  assert.equal(resolution.effectiveMethod, "DELETE");
  assert.equal(resolution.overrideApplied, true);
});

test("explicit trust callback enables override without an origin header", async () => {
  const decision = await authorizeRequest(
    post({ headers: { "x-http-method-override": "DELETE" } }),
    {
      enabled: true,
      allowSameOrigin: false,
      isTrustedSource: (request) => request.headers?.["x-trusted-gateway"] === "yes",
    },
    allowAll,
  );

  assert.equal(decision.effectiveMethod, "POST");

  const trustedDecision = await authorizeRequest(
    {
      ...post({ headers: { "x-http-method-override": "DELETE", "x-trusted-gateway": "yes" } }),
      origin: EVIL_ORIGIN,
    },
    {
      enabled: true,
      allowSameOrigin: false,
      isTrustedSource: () => true,
    },
    allowAll,
  );
  assert.equal(trustedDecision.effectiveMethod, "DELETE");
});

test("CSRF, audit and rate-limit records are identical with and without override", () => {
  const plain = resolveRequestMethod(post(), trustedOptions);
  const overridden = resolveRequestMethod(
    post({ headers: { "x-http-method-override": "DELETE" } }),
    trustedOptions,
  );

  assert.deepEqual(plain.security, overridden.security);
  assert.deepEqual(plain.security, {
    csrf: { method: "POST" },
    audit: { method: "POST" },
    rateLimit: { method: "POST" },
  });
  assert.equal(overridden.csrfMethod, "POST");
  assert.equal(overridden.auditMethod, "POST");
  assert.equal(overridden.rateLimitMethod, "POST");
  assert.equal(overridden.audit.method, "POST");
  assert.equal(overridden.audit.effectiveMethod, "DELETE");
});

test("invalid override methods and forbidden override targets are rejected", () => {
  const invalid = resolveRequestMethod(
    post({ headers: { "x-http-method-override": "DELETE;" } }),
    trustedOptions,
  );
  assert.equal(invalid.valid, false);
  assert.equal(invalid.reasonCode, "INVALID_OVERRIDE_METHOD");

  const forbidden = resolveRequestMethod(
    post({ headers: { "x-http-method-override": "CONNECT" } }),
    trustedOptions,
  );
  assert.equal(forbidden.valid, false);
  assert.equal(forbidden.reasonCode, "OVERRIDE_METHOD_NOT_ALLOWED");
});

test("override from a non-POST transport method is not eligible by default", () => {
  const resolution = resolveRequestMethod(
    {
      method: "GET",
      url: API_URL,
      origin: APP_ORIGIN,
      headers: { "x-http-method-override": "DELETE" },
    },
    trustedOptions,
  );

  assert.equal(resolution.valid, true);
  assert.equal(resolution.effectiveMethod, "GET");
  assert.equal(resolution.reasonCode, "ORIGINAL_METHOD_NOT_ELIGIBLE");
});

test("an authorizer exception is a deny and does not prevent the other check", async () => {
  const calls: string[] = [];
  const decision = await authorizeRequest(
    post({ headers: { "x-http-method-override": "DELETE" } }),
    trustedOptions,
    (context) => {
      calls.push(context.method);
      if (context.method === "POST") throw new Error("policy backend unavailable");
      return true;
    },
  );

  assert.deepEqual(calls, ["POST", "DELETE"]);
  assert.equal(decision.allowed, false);
  assert.equal(decision.outcome, "deny");
  assert.equal(decision.authorization.checks[0].reason, "policy backend unavailable");
  assert.equal(decision.authorization.checks[1].allowed, true);
});

test("the reusable guard resolves and authorizes through the same policy", async () => {
  const guard = createMethodOverrideGuard(trustedOptions);

  const resolution = guard.resolve(
    post({ headers: { "x-http-method-override": "DELETE" } }),
  );
  assert.equal(resolution.effectiveMethod, "DELETE");

  const decision = await guard(
    post({ headers: { "x-http-method-override": "DELETE" } }),
    async (context) => context.method === "POST" || context.method === "DELETE",
  );
  assert.equal(decision.allowed, true);
  assert.deepEqual(decision.authorization.methods, ["POST", "DELETE"]);
});

test("header names are case-insensitive and methods are normalized", () => {
  assert.equal(normalizeMethod(" delete "), "DELETE");
  assert.equal(normalizeMethod("DE\nLETE"), null);

  const resolution = resolveRequestMethod(
    post({ headers: { "X-HTTP-METHOD-OVERRIDE": "delete" } }),
    trustedOptions,
  );
  assert.equal(resolution.declaredMethod, "DELETE");
  assert.equal(resolution.effectiveMethod, "DELETE");
});

test("Fetch Headers input supports override and conflict detection", () => {
  const headers = new Headers({ "X-HTTP-Method-Override": "DELETE" });
  const resolution = resolveRequestMethod({ method: "POST", origin: APP_ORIGIN, headers }, trustedOptions);

  assert.equal(resolution.effectiveMethod, "DELETE");
  assert.equal(getEffectiveMethod({ method: "POST", origin: APP_ORIGIN, headers }, trustedOptions), "DELETE");
  assert.equal(resolution.forwardHeaders["x-http-method-override"], undefined);

  const conflicting = new Headers();
  conflicting.append("x-http-method-override", "DELETE");
  conflicting.append("x-method-override", "PATCH");
  const rejected = resolveRequestMethod(
    { method: "POST", origin: APP_ORIGIN, headers: conflicting },
    trustedOptions,
  );
  assert.equal(rejected.reasonCode, "CONFLICTING_HEADER_METHODS");
});

test("a gateway-normalized trust marker is honored only when override is enabled", () => {
  const enabled = resolveRequestMethod(
    post({
      origin: EVIL_ORIGIN,
      trusted: true,
      headers: { "x-http-method-override": "DELETE" },
    }),
    { enabled: true, allowSameOrigin: false },
  );
  assert.equal(enabled.effectiveMethod, "DELETE");

  const disabled = resolveRequestMethod(
    post({
      origin: EVIL_ORIGIN,
      trusted: true,
      headers: { "x-http-method-override": "DELETE" },
    }),
    { allowSameOrigin: false },
  );
  assert.equal(disabled.effectiveMethod, "POST");
});

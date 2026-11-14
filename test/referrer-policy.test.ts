import test from "node:test";
import assert from "node:assert/strict";
import {
  DEFAULT_REFERRER_POLICY,
  REFERRER_POLICY_HEADER,
  SENSITIVE_REFERRER_POLICY,
  applyReferrerPolicy,
  buildReferrerPolicyHeaders,
  createReferrerPolicyMiddleware,
  createReferrerPortal,
  resolveReferrerPolicy,
  withReferrerPolicy,
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
    getHeader(name: string) {
      return headers[name] ?? null;
    },
    end(body = "") {
      this.body = body;
    },
  };
}

test("all responses receive a strict Referrer-Policy by default", () => {
  assert.equal(
    resolveReferrerPolicy("/"),
    DEFAULT_REFERRER_POLICY,
  );
  assert.equal(DEFAULT_REFERRER_POLICY, "strict-origin-when-cross-origin");

  const headers = buildReferrerPolicyHeaders("/dashboard");
  assert.equal(headers[REFERRER_POLICY_HEADER], DEFAULT_REFERRER_POLICY);
});

test("email, help center and reset pages are graded no-referrer", () => {
  for (const path of [
    "/emails/welcome",
    "/email/preferences",
    "/help-center/article/123",
    "/help/faq",
    "/reset-password?token=abc",
    "/account/security",
    "/auth/login",
  ]) {
    assert.equal(
      resolveReferrerPolicy(path),
      SENSITIVE_REFERRER_POLICY,
      path,
    );
  }

  const headers = buildReferrerPolicyHeaders("/help?token=secret");
  assert.equal(headers[REFERRER_POLICY_HEADER], "no-referrer");
  assert.equal(headers["Cache-Control"], "no-store");
});

test("a missing or permissive header is replaced by middleware", () => {
  const req = { url: "/help/faq?article=1", method: "GET" };
  const missing = createNodeResponse();
  createReferrerPolicyMiddleware()(req, missing);
  assert.equal(missing.headers[REFERRER_POLICY_HEADER], "no-referrer");

  const permissive = createNodeResponse();
  permissive.headers[REFERRER_POLICY_HEADER] = "unsafe-url";
  createReferrerPolicyMiddleware()(req, permissive);
  assert.equal(permissive.headers[REFERRER_POLICY_HEADER], "no-referrer");
});

test("custom path rules are ordered and first-match-wins", () => {
  const policy = resolveReferrerPolicy("/docs/public", {
    rules: [
      { prefix: "/docs/public", policy: "origin" },
      { prefix: "/docs", policy: "no-referrer" },
    ],
  });
  assert.equal(policy, "origin");

  assert.equal(
    resolveReferrerPolicy("/docs/private", {
      rules: [{ prefix: "/docs", policy: "no-referrer" }],
    }),
    "no-referrer",
  );
});

test("a token-bearing URL is sensitive even on an otherwise public path", () => {
  assert.equal(
    resolveReferrerPolicy("/landing?reset_token=abc"),
    "no-referrer",
  );
  assert.equal(
    resolveReferrerPolicy("/landing#access_token=abc"),
    "no-referrer",
  );
});

test("fetch responses can be finalized with the policy header", async () => {
  const original = new Response("ok", {
    headers: { "Referrer-Policy": "unsafe-url" },
  });
  const finalized = applyReferrerPolicy(
    original,
    "/help",
  ) as Response;

  assert.equal(finalized.headers.get(REFERRER_POLICY_HEADER), "no-referrer");
  assert.equal(await finalized.text(), "ok");
});

test("withReferrerPolicy wraps fetch handlers", async () => {
  const handler = withReferrerPolicy(
    () => new Response("ok"),
  );
  const response = (await handler(
    new Request("https://portal.example/help?token=abc"),
  )) as Response;

  assert.equal(response.headers.get(REFERRER_POLICY_HEADER), "no-referrer");
  assert.equal(await response.text(), "ok");
});

test("portal middleware applies policy to ordinary and sensitive pages", () => {
  const portal = createReferrerPortal();

  const ordinary = createNodeResponse();
  portal.middleware({ url: "/dashboard" }, ordinary);
  assert.equal(
    ordinary.headers[REFERRER_POLICY_HEADER],
    DEFAULT_REFERRER_POLICY,
  );

  const help = createNodeResponse();
  portal.middleware({ url: "/help" }, help);
  assert.equal(help.headers[REFERRER_POLICY_HEADER], "no-referrer");
});

test("applyReferrerPolicy accepts request-first and response-first order", () => {
  const req = { url: "/reset?token=abc", method: "GET" };

  const responseFirst = createNodeResponse();
  applyReferrerPolicy(responseFirst, req);
  assert.equal(
    responseFirst.headers[REFERRER_POLICY_HEADER],
    "no-referrer",
  );

  const requestFirst = createNodeResponse();
  applyReferrerPolicy(req, requestFirst);
  assert.equal(
    requestFirst.headers[REFERRER_POLICY_HEADER],
    "no-referrer",
  );
});

test("overwrite:false preserves an explicitly managed header", () => {
  const req = { url: "/help", method: "GET" };
  const res = createNodeResponse();
  res.headers[REFERRER_POLICY_HEADER] = "same-origin";

  applyReferrerPolicy(res, req, { overwrite: false });
  assert.equal(res.headers[REFERRER_POLICY_HEADER], "same-origin");
});

test("plain response header bags receive the policy", () => {
  const res = { status: 200, headers: {} as Record<string, string> };
  applyReferrerPolicy(res, "/help");
  assert.equal(res.headers[REFERRER_POLICY_HEADER], "no-referrer");
});

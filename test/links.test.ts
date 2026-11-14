import test from "node:test";
import assert from "node:assert/strict";
import {
  buildShareableUrl,
  classifyOutboundLink,
  createSafeAnchorProps,
  createReferrerPortal,
  createShareableUrl,
  createSafeLink,
  isExternalUrl,
  stripSensitiveParams,
} from "../index.ts";

const PORTAL = "https://portal.example";

test("token-like parameters never enter shareable links", () => {
  assert.equal(
    createShareableUrl(
      "https://portal.example/account?reset_token=abc&tab=security",
    ),
    "https://portal.example/account?tab=security",
  );
  assert.equal(
    buildShareableUrl("https://portal.example/reset?lang=en", {
      token: "secret",
      email: "user@example.com",
      safe: "1",
    }),
    "https://portal.example/reset?lang=en&safe=1",
  );
  assert.equal(
    stripSensitiveParams(
      "https://portal.example/email#access_token=abc&view=preferences",
    ),
    "https://portal.example/email#view=preferences",
  );
  assert.equal(
    stripSensitiveParams(
      "https://portal.example/help?q=hello%20world&token=abc&path=%2Fdocs",
    ),
    "https://portal.example/help?q=hello%20world&path=%2Fdocs",
  );
});

test("sensitive pages never render a direct external link", () => {
  const target =
    "https://third-party.example/collect?reset_token=abc&from=help";
  const portal = createReferrerPortal({ baseUrl: PORTAL });
  const decision = portal.resolveLink(target, { pagePath: "/help" });

  assert.equal(decision.blocked, false);
  assert.equal(decision.kind, "external-relay");
  assert.equal(decision.relayed, true);
  assert.equal(decision.external, true);
  assert.match(decision.href ?? "", /\/__referrer-relay\?t=/);
  assert.doesNotMatch(
    decision.href ?? "",
    /third-party|reset_token|reset=/,
  );

  const targetUrl = new URL(decision.href ?? "", PORTAL);
  assert.equal(targetUrl.searchParams.has("t"), true);
  assert.equal(targetUrl.searchParams.has("url"), false);
});

test("sensitive pages can explicitly block external links", () => {
  const portal = createReferrerPortal({
    baseUrl: PORTAL,
    sensitiveExternalMode: "block",
  });
  const decision = portal.resolveLink(
    "https://third-party.example/collect?token=abc",
    { pagePath: "/email" },
  );

  assert.equal(decision.blocked, true);
  assert.equal(decision.href, null);
  assert.equal(decision.reason, "external-blocked-on-sensitive-page");
  assert.equal(portal.createLink("https://third-party.example/", {
    pagePath: "/email",
  }), null);
});

test("legitimate external links keep safe parameters and lose tokens", () => {
  const decision = classifyOutboundLink(
    "https://docs.example/guide?topic=security&token=leak&utm_source=portal",
    {
      baseUrl: PORTAL,
      pagePath: "/dashboard",
    },
  );

  assert.equal(decision.blocked, false);
  assert.equal(decision.kind, "external");
  assert.equal(
    decision.href,
    "https://docs.example/guide?topic=security&utm_source=portal",
  );
  assert.equal(decision.rel, "noopener noreferrer");
  assert.equal(decision.referrerPolicy, "strict-origin-when-cross-origin");

  const props = createSafeAnchorProps(
    "https://docs.example/guide?topic=security&token=leak",
    { baseUrl: PORTAL, pagePath: "/dashboard" },
  );
  assert.equal(props.href, "https://docs.example/guide?topic=security");
  assert.match(props.rel, /noreferrer/);
  assert.equal(props.referrerPolicy, "strict-origin-when-cross-origin");
});

test("third-party destinations never receive token parameters", () => {
  for (const target of [
    "https://logs.example/collect?token=abc",
    "https://logs.example/collect?access_token=abc",
    "https://logs.example/collect?resetToken=abc",
    "https://logs.example/collect?signature=abc",
    "https://logs.example/collect?code=abc",
    "https://logs.example/collect?api_key=abc",
  ]) {
    const decision = classifyOutboundLink(target, {
      baseUrl: PORTAL,
      pagePath: "/help",
      allowExternalOnSensitive: true,
    });
    assert.equal(decision.blocked, false);
    if (decision.kind === "external-relay") {
      assert.doesNotMatch(decision.href ?? "", /abc|logs\.example/);
    } else {
      assert.doesNotMatch(decision.href ?? "", /token|signature|code|api_key|abc/);
    }
  }
});

test("javascript and other unsafe schemes are never rendered", () => {
  for (const target of [
    "javascript:alert(document.cookie)",
    "data:text/html,<script>alert(1)</script>",
    "vbscript:msgbox(1)",
  ]) {
    const decision = classifyOutboundLink(target, {
      baseUrl: PORTAL,
      pagePath: "/help",
    });
    assert.equal(decision.blocked, true);
    assert.equal(decision.href, null);
  }
});

test("same-origin detection handles relative and protocol-relative URLs", () => {
  assert.equal(isExternalUrl("/help/faq", PORTAL), false);
  assert.equal(isExternalUrl("https://portal.example/help", PORTAL), false);
  assert.equal(isExternalUrl("//portal.example/help", PORTAL), false);
  assert.equal(isExternalUrl("https://portal.example.evil.test/help", PORTAL), true);
  assert.equal(isExternalUrl("https://other.example/help", PORTAL), true);
});

test("internal links are sanitized and marked noreferrer", () => {
  const href = createSafeLink("/account?token=abc&tab=security", {
    baseUrl: PORTAL,
    pagePath: "/account",
  });
  assert.equal(href, "/account?tab=security");
});

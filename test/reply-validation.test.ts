import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  handleWsignin,
  validateWreply,
  WreplyValidationError,
  WsigninRequestError,
  type RelyingParty,
} from "../index.ts";

const party: RelyingParty = {
  realm: "https://app.example.com/",
  replyAddresses: [
    "https://app.example.com/auth/callback",
    "https://app.example.com:443/sp/",
  ],
};

const registry: RelyingParty[] = [
  party,
  {
    realm: "https://legacy.example.net/",
    replyAddresses: ["http://legacy.example.net:8080/login"],
  },
];

function assertRejected(wreply: string, target: RelyingParty = party): void {
  assert.throws(
    () => validateWreply(wreply, target),
    WreplyValidationError,
    `expected rejection: ${wreply}`,
  );
}

describe("legitimate wreply values", () => {
  it("accepts an exact registered reply address", () => {
    const url = validateWreply("https://app.example.com/auth/callback", party);
    assert.equal(url.toString(), "https://app.example.com/auth/callback");
  });

  it("accepts a sub-path under a registered prefix", () => {
    const url = validateWreply(
      "https://app.example.com/auth/callback/tenant/42",
      party,
    );
    assert.equal(url.pathname, "/auth/callback/tenant/42");
  });

  it("accepts query strings and fragments on a valid reply", () => {
    const url = validateWreply(
      "https://app.example.com/auth/callback?foo=bar#frag",
      party,
    );
    assert.equal(url.search, "?foo=bar");
  });

  it("treats an explicit default port as equal to the registered port", () => {
    const url = validateWreply(
      "https://app.example.com:443/sp/endpoint",
      party,
    );
    assert.equal(url.hostname, "app.example.com");
  });

  it("accepts a non-default registered port exactly", () => {
    const url = validateWreply(
      "http://legacy.example.net:8080/login",
      registry[1],
    );
    assert.equal(url.port, "8080");
  });
});

describe("open redirect attempts", () => {
  it("rejects a different host", () => {
    assertRejected("https://evil.com/auth/callback");
  });

  it("rejects a look-alike host suffix", () => {
    assertRejected("https://app.example.com.evil.com/auth/callback");
  });

  it("rejects userinfo that hides the real host", () => {
    assertRejected("https://app.example.com@evil.com/auth/callback");
  });

  it("rejects a backslash that browsers collapse into a path slash", () => {
    assertRejected("https://app.example.com\\@evil.com/");
  });

  it("rejects an encoded backslash", () => {
    assertRejected("https://app.example.com/%5c%5cevil.com/");
  });

  it("rejects a different scheme", () => {
    assertRejected("http://app.example.com/auth/callback");
  });

  it("rejects non-http schemes", () => {
    assertRejected("javascript:alert(1)");
    assertRejected("data:text/html,<script>1</script>");
  });

  it("rejects a different port", () => {
    assertRejected("https://app.example.com:8443/auth/callback");
  });

  it("rejects a path outside every registered prefix", () => {
    assertRejected("https://app.example.com/admin/panel");
  });

  it("rejects a prefix that only matches as a string, not a segment", () => {
    assertRejected("https://app.example.com/auth/callback-evil");
  });

  it("rejects dot-dot traversal in the raw path", () => {
    assertRejected("https://app.example.com/auth/callback/../../admin");
  });

  it("rejects encoded dot-dot traversal", () => {
    assertRejected("https://app.example.com/auth/%2e%2e/admin");
    assertRejected("https://app.example.com/auth/%2E%2E%2fadmin");
  });

  it("rejects double-encoded traversal", () => {
    assertRejected("https://app.example.com/auth/%252e%252e/admin");
  });

  it("rejects malformed percent-encoding", () => {
    assertRejected("https://app.example.com/auth/callback/%zz");
  });

  it("rejects control characters in the path", () => {
    assertRejected("https://app.example.com/auth/callback/%00.evil");
  });

  it("rejects relative URLs", () => {
    assertRejected("/auth/callback");
    assertRejected("//evil.com/auth/callback");
  });

  it("rejects an empty wreply", () => {
    assertRejected("");
  });
});

describe("wsignin1.0 request handling", () => {
  it("returns the registered realm as the token audience", () => {
    const decision = handleWsignin(
      {
        wa: "wsignin1.0",
        wtrealm: "https://app.example.com/",
        wreply: "https://app.example.com/auth/callback",
      },
      registry,
    );
    assert.equal(decision.audience, "https://app.example.com/");
    assert.equal(decision.replyTo, "https://app.example.com/auth/callback");
  });

  it("falls back to the first registered reply address without wreply", () => {
    const decision = handleWsignin(
      { wa: "wsignin1.0", wtrealm: "https://app.example.com/" },
      registry,
    );
    assert.equal(decision.replyTo, "https://app.example.com/auth/callback");
  });

  it("rejects an attacker-controlled wreply on a valid realm", () => {
    assert.throws(
      () =>
        handleWsignin(
          {
            wa: "wsignin1.0",
            wtrealm: "https://app.example.com/",
            wreply: "https://evil.com/steal",
          },
          registry,
        ),
      WreplyValidationError,
    );
  });

  it("rejects an unregistered wtrealm", () => {
    assert.throws(
      () =>
        handleWsignin(
          {
            wa: "wsignin1.0",
            wtrealm: "https://evil.com/",
            wreply: "https://evil.com/collect",
          },
          registry,
        ),
      WsigninRequestError,
    );
  });

  it("rejects a wrong wa action", () => {
    assert.throws(
      () =>
        handleWsignin(
          { wa: "wsignout1.0", wtrealm: "https://app.example.com/" },
          registry,
        ),
      WsigninRequestError,
    );
  });
});

import assert from "node:assert/strict";
import { test } from "node:test";
import {
  parseHttpsOrigin,
  verifyChallengeOrigin,
  type RequestContext,
} from "../src/private-token/origin.ts";
import {
  ChallengeParseError,
  OriginVerificationError,
} from "../src/private-token/errors.ts";

const embeddedRequest: RequestContext = {
  requestOrigin: "https://embedded.example",
  topLevelOrigin: "https://top.example",
  isTopLevelNavigation: false,
};

test("non-empty origin_info requires the request origin to be listed", () => {
  const allowed = ["https://embedded.example", "https://other.example"];
  assert.doesNotThrow(() => verifyChallengeOrigin(allowed, embeddedRequest));

  assert.throws(
    () => verifyChallengeOrigin(["https://attacker.example"], embeddedRequest),
    OriginVerificationError,
  );
});

test("origin matching is origin-level and ignores default port spelling", () => {
  assert.doesNotThrow(() =>
    verifyChallengeOrigin(
      ["https://embedded.example:443"],
      { ...embeddedRequest, requestOrigin: "https://embedded.example" },
    ),
  );
  assert.throws(
    () =>
      verifyChallengeOrigin(
        ["https://embedded.example:8443"],
        embeddedRequest,
      ),
    OriginVerificationError,
  );
});

test("empty origin_info is restricted to same-origin top-level navigation", () => {
  const topLevelSameOrigin: RequestContext = {
    requestOrigin: "https://top.example",
    topLevelOrigin: "https://top.example",
    isTopLevelNavigation: true,
  };
  assert.doesNotThrow(() => verifyChallengeOrigin([], topLevelSameOrigin));

  assert.throws(
    () => verifyChallengeOrigin([], embeddedRequest),
    OriginVerificationError,
  );

  const crossSiteTopLevel: RequestContext = {
    requestOrigin: "https://embedded.example",
    topLevelOrigin: "https://top.example",
    isTopLevelNavigation: true,
  };
  assert.throws(
    () => verifyChallengeOrigin([], crossSiteTopLevel),
    OriginVerificationError,
  );
});

test("non-https request origins are rejected before any allowlist check", () => {
  assert.throws(
    () =>
      verifyChallengeOrigin(
        ["https://embedded.example"],
        { ...embeddedRequest, requestOrigin: "http://embedded.example" },
      ),
    OriginVerificationError,
  );
});

test("parseHttpsOrigin enforces origin shape", () => {
  assert.equal(parseHttpsOrigin("https://host.example:8443").serialized, "https://host.example:8443");
  assert.throws(() => parseHttpsOrigin("http://host.example"), ChallengeParseError);
  assert.throws(() => parseHttpsOrigin("https://host.example/path"), ChallengeParseError);
  assert.throws(() => parseHttpsOrigin("https://user@host.example"), ChallengeParseError);
  assert.throws(() => parseHttpsOrigin("not a url"), ChallengeParseError);
});

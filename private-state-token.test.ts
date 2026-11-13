import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";
import test from "node:test";

import {
  createIssuanceAdapter,
  normalizeOrigin,
  originsEqual,
  PrivateStateTokenService,
} from "./index.ts";

const ISSUER = "https://issuer.example";
const BOUND_ORIGIN = "https://shop.example.com";
const PARENT_ORIGIN = "https://example.com";
const SUBDOMAIN_ORIGIN = "https://checkout.shop.example.com";
const SIBLING_ORIGIN = "https://blog.example.com";
const NOW = 1_700_000_000_000;

function makeService(clock = () => NOW): PrivateStateTokenService {
  return new PrivateStateTokenService({
    issuer: ISSUER,
    secret: randomBytes(32),
    clock,
  });
}

function issueToken(
  service: PrivateStateTokenService,
  origin = BOUND_ORIGIN,
) {
  return createIssuanceAdapter(service).issue(origin);
}

test("origin comparison uses the complete canonical origin tuple", () => {
  assert.equal(
    normalizeOrigin("https://SHOP.example.com:443"),
    BOUND_ORIGIN,
  );
  assert.equal(originsEqual(BOUND_ORIGIN, "https://shop.example.com:443"), true);
  assert.equal(originsEqual(BOUND_ORIGIN, PARENT_ORIGIN), false);
  assert.equal(originsEqual(BOUND_ORIGIN, SUBDOMAIN_ORIGIN), false);
  assert.equal(originsEqual(BOUND_ORIGIN, SIBLING_ORIGIN), false);
  assert.equal(
    originsEqual(BOUND_ORIGIN, "https://shop.example.com.evil.example"),
    false,
  );
  assert.equal(
    originsEqual(BOUND_ORIGIN, "https://evilshop.example.com"),
    false,
  );
  assert.equal(originsEqual(BOUND_ORIGIN, "http://shop.example.com"), false);
});

test("first redemption on the issuance-bound origin succeeds", () => {
  const service = makeService();
  const token = issueToken(service);

  assert.deepEqual(service.redeem(token, BOUND_ORIGIN), {
    ok: true,
    issuer: ISSUER,
    origin: BOUND_ORIGIN,
  });
});

test("redemption on the parent domain is rejected without consuming the token", () => {
  const service = makeService();
  const token = issueToken(service);

  assert.deepEqual(service.redeem(token, PARENT_ORIGIN), {
    ok: false,
    reason: "origin-mismatch",
  });

  assert.equal(service.redeem(token, BOUND_ORIGIN).ok, true);
});

test("redemption on a subdomain is rejected without consuming the token", () => {
  const service = makeService();
  const token = issueToken(service);

  assert.deepEqual(service.redeem(token, SUBDOMAIN_ORIGIN), {
    ok: false,
    reason: "origin-mismatch",
  });

  assert.equal(service.redeem(token, BOUND_ORIGIN).ok, true);
});

test("parent and subdomain are each tried once, then the exact origin succeeds", () => {
  const service = makeService();
  const token = issueToken(service);

  assert.deepEqual(service.redeem(token, PARENT_ORIGIN), {
    ok: false,
    reason: "origin-mismatch",
  });
  assert.deepEqual(service.redeem(token, SUBDOMAIN_ORIGIN), {
    ok: false,
    reason: "origin-mismatch",
  });
  assert.equal(service.redeem(token, BOUND_ORIGIN).ok, true);
});

test("redemption on a sibling origin is rejected", () => {
  const service = makeService();
  const token = issueToken(service);

  assert.deepEqual(service.redeem(token, SIBLING_ORIGIN), {
    ok: false,
    reason: "origin-mismatch",
  });
});

test("different schemes and ports are different origins", () => {
  const service = makeService();
  const token = issueToken(service, "https://shop.example.com:8443");

  assert.deepEqual(service.redeem(token, "https://shop.example.com:8444"), {
    ok: false,
    reason: "origin-mismatch",
  });
  assert.deepEqual(service.redeem(token, "http://shop.example.com:8443"), {
    ok: false,
    reason: "origin-mismatch",
  });
  assert.equal(service.redeem(token, "https://shop.example.com:8443").ok, true);
});

test("a token tampered to change its bound origin is rejected", () => {
  const service = makeService();
  const token = issueToken(service);
  const tampered = { ...token, origin: PARENT_ORIGIN };

  assert.deepEqual(service.redeem(tampered, PARENT_ORIGIN), {
    ok: false,
    reason: "invalid-signature",
  });
});

test("the same token cannot be redeemed twice on the bound origin", () => {
  const service = makeService();
  const token = issueToken(service);

  assert.equal(service.redeem(token, BOUND_ORIGIN).ok, true);
  assert.deepEqual(service.redeem(token, BOUND_ORIGIN), {
    ok: false,
    reason: "already-redeemed",
  });
});

test("the issuance adapter delegates to the library and canonicalizes the origin", () => {
  const service = makeService();
  const adapter = createIssuanceAdapter(service);
  const token = adapter.issueToken("https://SHOP.example.com:443");

  assert.equal(token.origin, BOUND_ORIGIN);
  assert.equal(service.redeem(token, "https://shop.example.com").ok, true);
});

test("an expired token is rejected", () => {
  let now = NOW;
  const service = makeService(() => now);
  const token = issueToken(service);
  now = token.expiresAt;

  assert.deepEqual(service.redeem(token, BOUND_ORIGIN), {
    ok: false,
    reason: "expired",
  });
});

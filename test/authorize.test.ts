import { test } from "node:test";
import assert from "node:assert/strict";
import { TenantPolicy } from "../src/policy.ts";
import { authorizeAddress } from "../src/credentials.ts";
import type { Resolution } from "../src/resolver.ts";

const NOW = 10_000;

function makePolicy(extra: { deadlineMs?: number } = {}): TenantPolicy {
  return new TenantPolicy(
    "tenant-a",
    {
      rules: [{ cidr: "203.0.113.0/24", action: "allow" }],
      allowedSuffixes: ["example.com"],
      deadlineMs: extra.deadlineMs,
    },
    3,
  );
}

function resolution(chainTtls: number[], addrTtl: number, address = "203.0.113.10"): Resolution {
  const names = ["www.example.com", "edge.example.com", "cdn.example.com"];
  return {
    chain: chainTtls.map((ttlSec, i) => ({ name: names[i], ttlSec })),
    addresses: [{ address, ttlSec: addrTtl }],
  };
}

test("credential expiry uses the earliest cname hop ttl", () => {
  const result = authorizeAddress({
    policy: makePolicy(),
    hostname: "WWW.Example.Com.",
    port: 443,
    resolution: resolution([100, 5, 30], 60),
    address: "203.0.113.10",
    nowMs: NOW,
  });
  assert.ok(result.ok);
  assert.equal(result.credential.expiresAtMs, NOW + 5_000);
  assert.equal(result.credential.tenantId, "tenant-a");
  assert.equal(result.credential.hostname, "www.example.com");
  assert.equal(result.credential.address, "203.0.113.10");
  assert.equal(result.credential.port, 443);
  assert.equal(result.credential.generation, 3);
});

test("policy deadline caps the credential expiry", () => {
  const result = authorizeAddress({
    policy: makePolicy({ deadlineMs: NOW + 2_000 }),
    hostname: "www.example.com",
    port: 443,
    resolution: resolution([100], 60),
    address: "203.0.113.10",
    nowMs: NOW,
  });
  assert.ok(result.ok);
  assert.equal(result.credential.expiresAtMs, NOW + 2_000);
});

test("cname loop is rejected", () => {
  const result = authorizeAddress({
    policy: makePolicy(),
    hostname: "www.example.com",
    port: 443,
    resolution: {
      chain: [
        { name: "a.example.com", ttlSec: 60 },
        { name: "b.example.com", ttlSec: 60 },
        { name: "A.example.com.", ttlSec: 60 },
      ],
      addresses: [{ address: "203.0.113.10", ttlSec: 60 }],
    },
    address: "203.0.113.10",
    nowMs: NOW,
  });
  assert.ok(!result.ok);
  assert.equal(result.reason, "cname-loop");
});

test("hop outside the suffix whitelist is rejected", () => {
  const result = authorizeAddress({
    policy: makePolicy(),
    hostname: "www.example.com",
    port: 443,
    resolution: {
      chain: [
        { name: "www.example.com", ttlSec: 60 },
        { name: "cdn.evil.net", ttlSec: 60 },
      ],
      addresses: [{ address: "203.0.113.10", ttlSec: 60 }],
    },
    address: "203.0.113.10",
    nowMs: NOW,
  });
  assert.ok(!result.ok);
  assert.equal(result.reason, "suffix-not-allowed");
  assert.equal(result.detail, "cdn.evil.net");
});

test("denied address is rejected", () => {
  const result = authorizeAddress({
    policy: makePolicy(),
    hostname: "www.example.com",
    port: 443,
    resolution: resolution([60], 60, "198.51.100.8"),
    address: "198.51.100.8",
    nowMs: NOW,
  });
  assert.ok(!result.ok);
  assert.equal(result.reason, "address-denied");
});

test("ipv4-mapped address is normalized in the credential", () => {
  const result = authorizeAddress({
    policy: makePolicy(),
    hostname: "www.example.com",
    port: 443,
    resolution: resolution([60], 60, "::ffff:203.0.113.10"),
    address: "::ffff:203.0.113.10",
    nowMs: NOW,
  });
  assert.ok(result.ok);
  assert.equal(result.credential.address, "203.0.113.10");
});

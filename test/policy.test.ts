import { test } from "node:test";
import assert from "node:assert/strict";
import { TenantPolicy } from "../src/policy.ts";
import { canonicalIp } from "../src/address.ts";

const suffixes = ["example.com"];

function policy(rules: { cidr: string; action: "allow" | "deny" }[]): TenantPolicy {
  return new TenantPolicy("tenant", { rules, allowedSuffixes: suffixes }, 1);
}

test("longest prefix match wins", () => {
  const p = policy([
    { cidr: "10.0.0.0/8", action: "allow" },
    { cidr: "10.1.0.0/16", action: "deny" },
  ]);
  assert.equal(p.isAddressAllowed("10.2.3.4"), true);
  assert.equal(p.isAddressAllowed("10.1.2.3"), false);
});

test("tie at equal length prefers deny", () => {
  const p = policy([
    { cidr: "10.0.0.0/8", action: "allow" },
    { cidr: "10.0.0.0/8", action: "deny" },
  ]);
  assert.equal(p.isAddressAllowed("10.9.9.9"), false);
});

test("default deny when no rule matches", () => {
  const p = policy([{ cidr: "10.0.0.0/8", action: "allow" }]);
  assert.equal(p.isAddressAllowed("192.168.1.1"), false);
  assert.equal(policy([]).isAddressAllowed("10.1.1.1"), false);
});

test("ipv6 rules only match ipv6 addresses", () => {
  const p = policy([{ cidr: "2001:db8::/32", action: "allow" }]);
  assert.equal(p.isAddressAllowed("2001:db8::1"), true);
  assert.equal(p.isAddressAllowed("2001:db9::1"), false);
  assert.equal(p.isAddressAllowed("10.0.0.1"), false);
});

test("ipv4-mapped ipv6 is judged as ipv4", () => {
  const p = policy([{ cidr: "203.0.113.0/24", action: "allow" }]);
  assert.equal(p.isAddressAllowed("::ffff:203.0.113.9"), true);
  assert.equal(p.isAddressAllowed("::ffff:198.51.100.9"), false);
  assert.equal(canonicalIp("::ffff:203.0.113.9"), "203.0.113.9");
});

test("ipv4-mapped cidr behaves as its ipv4 prefix", () => {
  const p = policy([{ cidr: "::ffff:203.0.113.0/24", action: "allow" }]);
  assert.equal(p.isAddressAllowed("203.0.113.7"), true);
  assert.equal(p.isAddressAllowed("203.0.114.7"), false);
});

test("hostname suffix whitelist", () => {
  const p = policy([]);
  assert.equal(p.allowsHostname("a.example.com"), true);
  assert.equal(p.allowsHostname("example.com"), true);
  assert.equal(p.allowsHostname("A.Example.COM."), true);
  assert.equal(p.allowsHostname("notexample.com"), false);
  assert.equal(p.allowsHostname("evil.com"), false);
});

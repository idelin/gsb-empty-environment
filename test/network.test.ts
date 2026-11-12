import { describe, it } from "node:test";
import assert from "node:assert/strict";

import {
  isIpLiteral,
  isPrivateAddress,
  normalizeHost,
  sameHost,
} from "../src/network.ts";

describe("isPrivateAddress", () => {
  it("accepts RFC 1918 ranges", () => {
    assert.equal(isPrivateAddress("10.0.0.1"), true);
    assert.equal(isPrivateAddress("10.255.255.255"), true);
    assert.equal(isPrivateAddress("172.16.0.1"), true);
    assert.equal(isPrivateAddress("172.31.255.255"), true);
    assert.equal(isPrivateAddress("192.168.1.10"), true);
    assert.equal(isPrivateAddress("192.168.0.0"), true);
  });

  it("rejects loopback, link-local, CGNAT and public addresses", () => {
    assert.equal(isPrivateAddress("127.0.0.1"), false);
    assert.equal(isPrivateAddress("169.254.169.254"), false);
    assert.equal(isPrivateAddress("100.64.0.1"), false);
    assert.equal(isPrivateAddress("172.15.0.1"), false);
    assert.equal(isPrivateAddress("172.32.0.1"), false);
    assert.equal(isPrivateAddress("8.8.8.8"), false);
    assert.equal(isPrivateAddress("0.0.0.0"), false);
    assert.equal(isPrivateAddress("255.255.255.255"), false);
  });

  it("accepts IPv6 unique-local addresses only", () => {
    assert.equal(isPrivateAddress("fd00::1"), true);
    assert.equal(isPrivateAddress("fc62:4833:d061:bbfc::1"), true);
    assert.equal(isPrivateAddress("::1"), false);
    assert.equal(isPrivateAddress("fe80::1"), false);
    assert.equal(isPrivateAddress("2606:4700:4700::1111"), false);
  });

  it("rejects malformed literals and DNS names", () => {
    assert.equal(isPrivateAddress("192.168.1.256"), false);
    assert.equal(isPrivateAddress("192.168.1"), false);
    assert.equal(isPrivateAddress("router.local"), false);
    assert.equal(isPrivateAddress(""), false);
  });
});

describe("isIpLiteral", () => {
  it("recognizes both address families", () => {
    assert.equal(isIpLiteral("192.168.0.1"), true);
    assert.equal(isIpLiteral("fd00::1"), true);
    assert.equal(isIpLiteral("example.com"), false);
    assert.equal(isIpLiteral("192.168.0.1.5"), false);
  });
});

describe("sameHost / normalizeHost", () => {
  it("compresses IPv6 forms canonically", () => {
    assert.equal(sameHost("fd00:0:0:0:0:0:0:1", "fd00::1"), true);
    assert.equal(normalizeHost("FD00::ABCD"), "fd00::abcd");
  });

  it("keeps IPv4 literals exact", () => {
    assert.equal(sameHost("192.168.1.2", "192.168.1.2"), true);
    assert.equal(sameHost("192.168.1.2", "192.168.1.3"), false);
  });
});

import { test } from "node:test";
import assert from "node:assert/strict";
import {
  classifyHost,
  parseIPv4,
  parseIPv6,
} from "../src/ip-address.ts";

test("standard dotted IPv4", () => {
  assert.equal(parseIPv4("127.0.0.1"), 0x7f000001);
  assert.equal(parseIPv4("8.8.8.8"), 0x08080808);
  assert.equal(parseIPv4("256.0.0.1"), null);
  assert.equal(parseIPv4("1.2.3.4.5"), null);
});

test("integer and non-standard IPv4 forms", () => {
  assert.equal(parseIPv4("2130706433"), 0x7f000001); // decimal integer
  assert.equal(parseIPv4("0x7f000001"), 0x7f000001); // hex integer
  assert.equal(parseIPv4("0177.0.0.1"), 0x7f000001); // octal part
  assert.equal(parseIPv4("127.1"), 0x7f000001); // two-part
  assert.equal(parseIPv4("0x7f.1"), 0x7f000001); // hex part + tail
  assert.equal(parseIPv4("127.0.1"), 0x7f000001); // three-part
  assert.equal(parseIPv4("4294967295"), 0xffffffff);
  assert.equal(parseIPv4("4294967296"), null); // 2^32 overflows
  assert.equal(parseIPv4("0x100000000"), null);
  assert.equal(parseIPv4("300.0.0.1"), null);
});

test("IPv6 parsing including embedded IPv4", () => {
  assert.ok(parseIPv6("::1"));
  assert.ok(parseIPv6("fe80::1"));
  assert.ok(parseIPv6("2001:db8::1"));
  assert.ok(parseIPv6("::ffff:127.0.0.1"));
  assert.ok(parseIPv6("::ffff:7f00:1"));
  assert.equal(parseIPv6(":::"), null);
  assert.equal(parseIPv6("1::2::3"), null);
  assert.equal(parseIPv6("::ffff:999.0.0.1"), null);
});

test("loopback classification", () => {
  assert.equal(classifyHost("127.0.0.1"), "loopback");
  assert.equal(classifyHost("127.1"), "loopback");
  assert.equal(classifyHost("2130706433"), "loopback");
  assert.equal(classifyHost("0x7f000001"), "loopback");
  assert.equal(classifyHost("0177.0.0.1"), "loopback");
  assert.equal(classifyHost("0.0.0.0"), "loopback");
  assert.equal(classifyHost("::1"), "loopback");
  assert.equal(classifyHost("[::1]"), "loopback");
  assert.equal(classifyHost("::"), "loopback");
});

test("IPv4-mapped and compatible IPv6 classification", () => {
  assert.equal(classifyHost("::ffff:127.0.0.1"), "loopback");
  assert.equal(classifyHost("::ffff:7f00:1"), "loopback");
  assert.equal(classifyHost("::ffff:10.1.2.3"), "private");
  assert.equal(classifyHost("::ffff:a9fe:1"), "link-local"); // 169.254.0.1
  assert.equal(classifyHost("::ffff:8.8.8.8"), "public");
  assert.equal(classifyHost("::7f00:1"), "loopback"); // compatible
});

test("private and link-local classification", () => {
  assert.equal(classifyHost("10.0.0.1"), "private");
  assert.equal(classifyHost("172.16.0.1"), "private");
  assert.equal(classifyHost("172.31.255.255"), "private");
  assert.equal(classifyHost("172.32.0.1"), "public");
  assert.equal(classifyHost("192.168.1.1"), "private");
  assert.equal(classifyHost("169.254.1.1"), "link-local");
  assert.equal(classifyHost("fc00::1"), "private");
  assert.equal(classifyHost("fd12:3456::1"), "private");
  assert.equal(classifyHost("fe80::1"), "link-local");
  assert.equal(classifyHost("febf::1"), "link-local");
});

test("public and non-IP hosts", () => {
  assert.equal(classifyHost("8.8.8.8"), "public");
  assert.equal(classifyHost("2001:4860:4860::8888"), "public");
  assert.equal(classifyHost("example.com"), null);
  assert.equal(classifyHost("127.0.0.1.evil.example"), null);
});

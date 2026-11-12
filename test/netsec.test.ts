import { test } from "node:test";
import assert from "node:assert/strict";
import {
  evaluateUrl,
  isPrivateOrNonGlobal,
  parseHttpsUrl,
  isAllowedHost,
} from "../index.ts";

test("parseHttpsUrl 只接受 https，拒绝 http 与其他 scheme", () => {
  assert.ok(parseHttpsUrl("https://rdap.example/domain/x"));
  assert.equal(parseHttpsUrl("http://rdap.example/domain/x"), null);
  assert.equal(parseHttpsUrl("ftp://rdap.example/x"), null);
  assert.equal(parseHttpsUrl("//rdap.example/x"), null);
  assert.equal(parseHttpsUrl("not a url"), null);
});

test("parseHttpsUrl 归一化主机名并剥离 IPv6 方括号", () => {
  assert.equal(parseHttpsUrl("https://RDAP.Example./x")?.host, "rdap.example");
  assert.equal(parseHttpsUrl("https://[2001:db8::1]/x")?.host, "2001:db8::1");
  assert.equal(parseHttpsUrl("https://[2001:db8::1]/x")?.ipVersion, 6);
});

test("isPrivateOrNonGlobal 覆盖内网/环回/链路本地/CGNAT/保留段", () => {
  const blocked = [
    "10.0.0.5",
    "10.255.255.255",
    "172.16.0.1",
    "172.31.255.255",
    "192.168.1.1",
    "127.0.0.1",
    "169.254.169.254", // 云元数据端点
    "100.64.0.9", // CGNAT
    "0.0.0.0",
    "224.0.0.1",
    "255.255.255.255",
    "198.18.0.1",
    "::1",
    "::",
    "fe80::1",
    "fc00::1",
    "fd12:3456::1",
    "ff02::1",
    "2001:db8::1",
    "::ffff:192.168.0.1", // IPv4 映射的私有地址
    "::ffff:127.0.0.1",
  ];
  for (const ip of blocked) {
    assert.equal(isPrivateOrNonGlobal(ip), true, `应拦截 ${ip}`);
  }

  const allowed = [
    "8.8.8.8",
    "1.1.1.1",
    "2606:4700:4700::1111",
    "2001:4860:4860::8888",
  ];
  for (const ip of allowed) {
    assert.equal(isPrivateOrNonGlobal(ip), false, `不应拦截公网 ${ip}`);
  }
});

test("isAllowedHost 大小写不敏感，且白名单中的私有 IP 仍被拒绝", () => {
  const allowed = new Set(["rdap.example", "1.2.3.4"]);
  assert.equal(isAllowedHost("RDAP.EXAMPLE", allowed), true);
  assert.equal(isAllowedHost("1.2.3.4", allowed), true);
  assert.equal(isAllowedHost("evil.example", allowed), false);

  const withIntranet = new Set(["10.0.0.1"]);
  assert.equal(isAllowedHost("10.0.0.1", withIntranet), false);
});

test("evaluateUrl 拦截内网链接、非 https、未授权主机", () => {
  const allowed = new Set(["rdap.verisign.com", "whitelist.test"]);

  const intranet = evaluateUrl("https://192.168.1.10/metadata", allowed);
  assert.equal(intranet.ok, false);
  assert.equal(intranet.reason, "private-address");

  const loopback = evaluateUrl("https://127.0.0.1:8443/x", allowed);
  assert.equal(loopback.ok, false);
  assert.equal(loopback.reason, "private-address");

  const http = evaluateUrl("http://rdap.verisign.com/x", allowed);
  assert.equal(http.ok, false);
  assert.equal(http.reason, "not-https");

  const foreign = evaluateUrl("https://attacker.example/steal", allowed);
  assert.equal(foreign.ok, false);
  assert.equal(foreign.reason, "host-not-allowed");

  const ok = evaluateUrl("https://rdap.verisign.com/com/v1/domain/x", allowed);
  assert.equal(ok.ok, true);

  const whitelisted = evaluateUrl("https://whitelist.test/link", allowed);
  assert.equal(whitelisted.ok, true);
});

test("evaluateUrl 支持相对链接基于当前应答 URL 解析", () => {
  const allowed = new Set(["rdap.verisign.com"]);
  const verdict = evaluateUrl(
    "domain/foo.com",
    allowed,
    "https://rdap.verisign.com/com/v1/",
  );
  assert.equal(verdict.ok, true);
  assert.equal(
    verdict.parsed?.url.toString(),
    "https://rdap.verisign.com/com/v1/domain/foo.com",
  );
});

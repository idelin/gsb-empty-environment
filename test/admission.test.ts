import { test } from "node:test";
import assert from "node:assert/strict";
import {
  admitResponse,
  buildRdapUrl,
  filterFollowableLinks,
} from "../index.ts";
import { COM_BASE, domainResponse } from "./fixtures.ts";

test("admitResponse 接受与查询对象一致的 domain 应答", () => {
  const body = domainResponse("xn--bcher-kva.example");
  const result = admitResponse(body, "Bücher.Example");
  assert.equal(result.admitted, true);
  assert.equal(result.violation, undefined);
});

test("admitResponse 拒绝错类型对象（objectClassName 不匹配）", () => {
  const nameserver = domainResponse("victim.com", { objectClassName: "nameserver" });
  assert.equal(admitResponse(nameserver, "victim.com").violation, "object-class-mismatch");

  const entity = domainResponse("victim.com", { objectClassName: "entity" });
  assert.equal(admitResponse(entity, "victim.com").violation, "object-class-mismatch");
});

test("admitResponse 拒绝 ldhName 与查询名不一致（含 IDN 大小写变体）", () => {
  const wrong = domainResponse("attacker.com");
  assert.equal(admitResponse(wrong, "victim.com").violation, "ldh-name-mismatch");

  // 攻击者用大写 Punycode 试图混淆，归一化后仍须能正确比较/拒绝。
  const caseVariant = domainResponse("XN--BCHER-KVA.example");
  assert.equal(admitResponse(caseVariant, "bücher.example").admitted, true);

  const missing = domainResponse("x", { ldhName: undefined });
  assert.equal(admitResponse(missing, "x").violation, "ldh-name-missing");
});

test("admitResponse 校验 rdapConformance", () => {
  const noConf = domainResponse("victim.com", { rdapConformance: [] });
  assert.equal(admitResponse(noConf, "victim.com").violation, "conformance-missing");

  const wrongLevel = domainResponse("victim.com", { rdapConformance: ["some-other-level"] });
  assert.equal(admitResponse(wrongLevel, "victim.com").violation, "conformance-mismatch");
});

test("admitResponse 拒绝非对象载荷", () => {
  assert.equal(admitResponse(null, "victim.com").violation, "not-an-object");
  assert.equal(admitResponse([], "victim.com").violation, "not-an-object");
  assert.equal(admitResponse("text", "victim.com").violation, "not-an-object");
});

test("filterFollowableLinks 只保留 https 且主机在准入集合内的链接", () => {
  const allowed = new Set(["rdap.verisign.com"]);
  const links = [
    { rel: "self", href: "https://rdap.verisign.com/com/v1/domain/a.com" },
    { rel: "related", href: "http://rdap.verisign.com/insecure" },
    { rel: "malicious", href: "https://192.168.0.9/internal" },
    { rel: "malicious", href: "https://attacker.test/steal" },
    { rel: "broken" },
    "not-an-object",
  ];
  const safe = filterFollowableLinks(links, allowed, COM_BASE);
  assert.equal(safe.length, 1);
  assert.equal(safe[0].rel, "self");
  assert.equal(safe[0].parsed.host, "rdap.verisign.com");
});

test("buildRdapUrl 用小写 Punycode 拼 domain 查询路径", () => {
  const url = buildRdapUrl(new URL(COM_BASE), "Bücher.COM");
  assert.equal(
    url?.toString(),
    "https://rdap.verisign.com/com/v1/domain/xn--bcher-kva.com",
  );
  assert.equal(buildRdapUrl(new URL(COM_BASE), "bad name"), null);
});

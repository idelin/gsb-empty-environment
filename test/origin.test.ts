import { test } from "node:test";
import assert from "node:assert/strict";

import {
  cloneOrigin,
  isOpaque,
  opaqueOrigin,
  parseTupleOrigin,
  sameOrigin,
  serializeOrigin,
  tupleOrigin,
} from "../index.ts";

test("普通来源按 scheme/host/port 结构化比较，大小写规范化不影响同源", () => {
  const lower = tupleOrigin("https", "Example.COM", 443);
  const upper = tupleOrigin("HTTPS", "example.com");
  assert.equal(lower.scheme, "https");
  assert.equal(lower.host, "example.com");
  assert.equal(lower.port, 443);
  assert.ok(sameOrigin(lower, upper));
  assert.equal(serializeOrigin(lower), "https://example.com");
});

test("默认端口：省略端口与显式默认端口同源，非默认端口不同源", () => {
  const implicit = tupleOrigin("https", "app.example.test");
  const explicitDefault = tupleOrigin("https", "app.example.test", 443);
  const custom = tupleOrigin("https", "app.example.test", 8443);
  const httpDefault = tupleOrigin("http", "app.example.test");

  assert.equal(implicit.port, 443);
  assert.ok(sameOrigin(implicit, explicitDefault));
  assert.equal(false, sameOrigin(implicit, custom));
  assert.equal(false, sameOrigin(implicit, httpDefault));
  assert.equal(serializeOrigin(implicit), "https://app.example.test");
  assert.equal(serializeOrigin(custom), "https://app.example.test:8443");
  assert.equal(serializeOrigin(httpDefault), "http://app.example.test");
});

test("构造期拒绝非法端口与非 ASCII 主机", () => {
  assert.throws(() => tupleOrigin("https", "a.test", 0), TypeError);
  assert.throws(() => tupleOrigin("https", "a.test", 65536), TypeError);
  assert.throws(() => tupleOrigin("https", "a.test", 443.5), TypeError);
  assert.throws(() => tupleOrigin("https", "例え.test"), TypeError);
  assert.throws(() => tupleOrigin("https", "bad host"), TypeError);
  assert.throws(() => tupleOrigin("ht!tp", "a.test"), TypeError);
  assert.throws(() => tupleOrigin("custom", "a.test"), TypeError);
  assert.doesNotThrow(() => tupleOrigin("custom", "a.test", 9000));
});

test("IPv4 与 IPv6 主机的解析与比较", () => {
  const v4 = tupleOrigin("http", "127.0.0.1", 8080);
  const v6 = tupleOrigin("http", "[::1]", 8080);
  const v6Upper = tupleOrigin("http", "[2001:DB8::1]", 80);
  assert.equal(serializeOrigin(v4), "http://127.0.0.1:8080");
  assert.equal(serializeOrigin(v6), "http://[::1]:8080");
  assert.equal(serializeOrigin(v6Upper), "http://[2001:db8::1]");
  assert.equal(false, sameOrigin(v4, v6));
  assert.throws(() => tupleOrigin("http", "[::1"), TypeError);
  assert.throws(() => tupleOrigin("http", "999.1.1.1"), TypeError);
});

test("不透明来源：独立随机身份互不同源，尽管显示字符串都是 null", () => {
  const sandboxA = opaqueOrigin();
  const sandboxB = opaqueOrigin();

  assert.ok(isOpaque(sandboxA));
  assert.notEqual(sandboxA.identityId, sandboxB.identityId);
  assert.equal(false, sameOrigin(sandboxA, sandboxB));
  assert.equal(serializeOrigin(sandboxA), "null");
  assert.equal(serializeOrigin(sandboxB), "null");

  const inherited = cloneOrigin(sandboxA);
  assert.ok(sameOrigin(sandboxA, inherited), "继承同一身份的克隆体必须同源");
  assert.equal(false, sameOrigin(sandboxA, tupleOrigin("https", "null", 443)));
});

test("字符串 null 不能反序列化出任何不透明来源", () => {
  assert.throws(() => parseTupleOrigin("null"), /unforgeable/);
  const roundTrip = parseTupleOrigin(serializeOrigin(tupleOrigin("https", "a.test", 8443)));
  assert.ok(sameOrigin(roundTrip, tupleOrigin("https", "a.test", 8443)));
  const fromUrl = parseTupleOrigin("https://a.test:8443/path?x=1#frag");
  assert.ok(sameOrigin(fromUrl, tupleOrigin("https", "a.test", 8443)));
  assert.throws(() => parseTupleOrigin("not-a-url"), TypeError);
});

test("显式克隆继承身份；结构化克隆产生新的不透明身份（跨上下文不串域）", () => {
  const sandbox = opaqueOrigin();
  const inherited = cloneOrigin(sandbox);
  assert.ok(sameOrigin(sandbox, inherited));

  const acrossRealm = structuredClone(sandbox) as typeof sandbox;
  assert.equal(serializeOrigin(acrossRealm), "null");
  assert.equal(false, sameOrigin(sandbox, acrossRealm));
  assert.equal(false, sameOrigin(acrossRealm, structuredClone(sandbox)));
});

test("反模式演示：基于字符串的授权会把不同不透明来源混为一谈", () => {
  const trusted = opaqueOrigin();
  const attacker = opaqueOrigin();
  const insecureStringCheck = (origin: ReturnType<typeof serializeOrigin>) =>
    origin === "null";

  assert.equal(serializeOrigin(trusted), serializeOrigin(attacker));
  assert.equal(
    insecureStringCheck(serializeOrigin(attacker)),
    insecureStringCheck(serializeOrigin(trusted)),
  );
  assert.equal(false, sameOrigin(trusted, attacker), "结构化比较不会串域");
});

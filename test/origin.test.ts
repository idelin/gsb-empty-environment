import { test } from "node:test";
import assert from "node:assert/strict";
import { classifyRelationship, parseOrigin } from "../index.ts";

const doc = parseOrigin("https://app.example.com")!;

test("解析结构化来源并规范化默认端口", () => {
  assert.deepEqual(parseOrigin("https://app.example.com/path?q=1"), {
    scheme: "https",
    host: "app.example.com",
    port: null,
  });
  assert.deepEqual(parseOrigin("http://app.example.com:8080"), {
    scheme: "http",
    host: "app.example.com",
    port: 8080,
  });
});

test("非法或非 http(s) URL 返回 null", () => {
  assert.equal(parseOrigin("not-a-url"), null);
  assert.equal(parseOrigin("data:text/plain,hi"), null);
});

test("来源关系分类：同源 / 同站跨源 / 跨站", () => {
  assert.equal(
    classifyRelationship(doc, parseOrigin("https://app.example.com/x")!),
    "same-origin",
  );
  assert.equal(
    classifyRelationship(doc, parseOrigin("https://cdn.example.com")!),
    "same-site",
  );
  assert.equal(
    classifyRelationship(doc, parseOrigin("https://other.example.org")!),
    "cross-site",
  );
  assert.equal(
    classifyRelationship(doc, parseOrigin("http://app.example.com")!),
    "cross-site",
  );
  assert.equal(
    classifyRelationship(doc, parseOrigin("https://app.example.com:8443")!),
    "same-site",
  );
});

test("多标签公共后缀参与同站判断", () => {
  const uk = parseOrigin("https://shop.example.co.uk")!;
  assert.equal(
    classifyRelationship(uk, parseOrigin("https://assets.example.co.uk")!),
    "same-site",
  );
  assert.equal(
    classifyRelationship(uk, parseOrigin("https://example.co.uk")!),
    "same-site",
  );
  assert.equal(
    classifyRelationship(uk, parseOrigin("https://shop.other.co.uk")!),
    "cross-site",
  );
});

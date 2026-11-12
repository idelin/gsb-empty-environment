import assert from "node:assert/strict";
import { test } from "node:test";
import { parseXml } from "../src/xml-parser.ts";
import { XmlParseError } from "../src/errors.ts";

test("按命名空间 URI 解析元素与属性，不依赖前缀", () => {
  const doc = parseXml(
    `<r xmlns:foo="urn:x" xmlns="urn:default">` +
      `<foo:child foo:id="1" plain="2"/>` +
      `</r>`,
  );
  assert.equal(doc.root.namespaceUri, "urn:default");
  const child = doc.root.children[0];
  assert.equal(child.kind, "element");
  if (child.kind !== "element") return;
  assert.equal(child.namespaceUri, "urn:x");
  assert.equal(child.localName, "child");
  assert.equal(child.prefix, "foo");
  const idAttr = child.attributes.find((a) => a.localName === "id");
  assert.equal(idAttr?.namespaceUri, "urn:x");
  assert.equal(
    child.attributes.find((a) => a.localName === "plain")?.namespaceUri,
    null,
  );
});

test("默认命名空间对无前缀元素生效、对无前缀属性不生效", () => {
  const doc = parseXml(`<r xmlns="urn:d"><c a="1"/></r>`);
  const child = doc.root.children[0];
  assert.equal(child.kind, "element");
  if (child.kind !== "element") return;
  assert.equal(child.namespaceUri, "urn:d");
  assert.equal(child.attributes[0].namespaceUri, null);
});

test("解析内置实体、数字字符引用与 CDATA", () => {
  const doc = parseXml(
    `<r>a&amp;b&lt;c&#65;x&#x42;y<![CDATA[<raw>&]]></r>`,
  );
  const text = doc.root.children[0];
  assert.equal(text.kind, "text");
  if (text.kind !== "text") return;
  assert.equal(text.text, "a&b<cAxBy<raw>&");
});

test("拒绝 DOCTYPE，阻断 XXE/DTD 攻击面", () => {
  assert.throws(
    () =>
      parseXml(
        `<!DOCTYPE r [<!ENTITY x SYSTEM "file:///etc/passwd">]><r>&x;</r>`,
      ),
    XmlParseError,
  );
});

test("拒绝未定义实体", () => {
  assert.throws(() => parseXml(`<r>&undeclared;</r>`), XmlParseError);
});

test("拒绝未绑定的命名空间前缀", () => {
  assert.throws(() => parseXml(`<r><x:y/></r>`), XmlParseError);
});

test("拒绝非法字符引用与标签不匹配", () => {
  assert.throws(() => parseXml(`<r>&#0;</r>`), XmlParseError);
  assert.throws(() => parseXml(`<a></b>`), XmlParseError);
});

test("xml:base 以 XML 命名空间 URI 标识", () => {
  const doc = parseXml(`<r xml:base="/etc/" xmlns:xml="${"http://www.w3.org/XML/1998/namespace"}"><c/></r>`);
  const base = doc.root.attributes.find((a) => a.localName === "base");
  assert.equal(base?.namespaceUri, "http://www.w3.org/XML/1998/namespace");
  assert.equal(base?.value, "/etc/");
});

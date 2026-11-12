import assert from "node:assert/strict";
import { test } from "node:test";
import { parseXml } from "../src/xml-parser.ts";
import { serializeXml } from "../src/xml-serializer.ts";

test("序列化保留命名空间声明并剥离 xml:base", () => {
  const doc = parseXml(
    `<r xmlns:xi="http://www.w3.org/2001/XInclude" xml:base="/etc/" id="1"/>`,
  );
  const output = serializeXml(doc.root);
  assert.match(output, /xmlns:xi="http:\/\/www.w3.org\/2001\/XInclude"/);
  assert.match(output, /id="1"/);
  assert.doesNotMatch(output, /xml:base/);
});

test("文本与属性被转义", () => {
  const doc = parseXml(`<r a="x&quot;y">5 &lt; 10 &amp; ok</r>`);
  const output = serializeXml(doc.root);
  assert.match(output, /a="x&quot;y"/);
  assert.match(output, />5 &lt; 10 &amp; ok</);
});

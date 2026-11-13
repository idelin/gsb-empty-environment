import assert from "node:assert/strict";
import test from "node:test";

import {
  extractEncoding,
  isHtmlIntegrationPointEncoding,
  parseAnnotationXml,
  parseAnnotationXmlTag,
  type ElementNode,
  type Node,
} from "./index.ts";

function elementNames(nodes: Node[]): string[] {
  return nodes.filter((n) => n.type === "element").map((n) => (n as ElementNode).name);
}

test("只有 text/html 与 application/xhtml+xml 是 HTML 集成点", () => {
  assert.equal(isHtmlIntegrationPointEncoding("text/html"), true);
  assert.equal(isHtmlIntegrationPointEncoding("application/xhtml+xml"), true);
  assert.equal(isHtmlIntegrationPointEncoding("text/plain"), false);
  assert.equal(isHtmlIntegrationPointEncoding("image/svg+xml"), false);
  assert.equal(isHtmlIntegrationPointEncoding(""), false);
  assert.equal(isHtmlIntegrationPointEncoding(null), false);
  assert.equal(isHtmlIntegrationPointEncoding(undefined), false);
});

test("encoding 大小写不敏感", () => {
  assert.equal(isHtmlIntegrationPointEncoding("TEXT/HTML"), true);
  assert.equal(isHtmlIntegrationPointEncoding("Text/Html"), true);
  assert.equal(isHtmlIntegrationPointEncoding("APPLICATION/XHTML+XML"), true);
  assert.equal(isHtmlIntegrationPointEncoding("Application/Xhtml+Xml"), true);
  assert.equal(isHtmlIntegrationPointEncoding("TEXT/PLAIN"), false);
});

test("extractEncoding 支持双引号、单引号、无引号与大写属性名", () => {
  assert.equal(
    extractEncoding('<annotation-xml encoding="text/html">'),
    "text/html",
  );
  assert.equal(
    extractEncoding("<annotation-xml encoding='application/xhtml+xml'>"),
    "application/xhtml+xml",
  );
  assert.equal(
    extractEncoding("<annotation-xml encoding=text/plain>"),
    "text/plain",
  );
  assert.equal(
    extractEncoding('<annotation-xml ENCODING="TEXT/HTML">'),
    "TEXT/HTML",
  );
  assert.equal(extractEncoding("<annotation-xml>"), null);
});

test("HTML 集成点按 HTML 解析并生成元素子树", () => {
  const nodes = parseAnnotationXml(
    "text/html",
    '<span class="k"><b>bold</b>tail</span>',
  );
  assert.deepEqual(elementNames(nodes), ["span"]);
  const span = nodes[0] as ElementNode;
  assert.equal(span.attributes.class, "k");
  assert.deepEqual(elementNames(span.children), ["b"]);
  const bold = span.children[0] as ElementNode;
  assert.deepEqual(bold.children, [{ type: "text", text: "bold" }]);
  assert.deepEqual(span.children[1], { type: "text", text: "tail" });
});

test("text/plain 不生成任何元素，可执行片段降级为文本", () => {
  const payload = "<script>alert(1)</script>";
  const nodes = parseAnnotationXml("text/plain", payload);
  assert.deepEqual(nodes, [{ type: "text", text: payload }]);
  assert.deepEqual(elementNames(nodes), []);
});

test("缺失或未知 encoding 一律当文本", () => {
  for (const encoding of [null, undefined, "", "image/svg+xml", "text/xml"]) {
    const nodes = parseAnnotationXml(encoding, "<img src=x onerror=alert(1)>");
    assert.deepEqual(elementNames(nodes), []);
    assert.equal(nodes[0].type, "text");
  }
});

test("交换大小写与引号后裁决与子树保持一致", () => {
  const content = "<em>x</em>";
  const variants = [
    '<annotation-xml encoding="text/html">',
    "<annotation-xml encoding='text/html'>",
    '<annotation-xml encoding="TEXT/HTML">',
    "<annotation-xml ENCODING='Text/Html'>",
    '<annotation-xml encoding="application/xhtml+xml">',
    "<annotation-xml encoding='APPLICATION/XHTML+XML'>",
  ];
  for (const tag of variants) {
    const nodes = parseAnnotationXmlTag(tag, content);
    assert.deepEqual(elementNames(nodes), ["em"], tag);
    const em = nodes[0] as ElementNode;
    assert.deepEqual(em.children, [{ type: "text", text: "x" }], tag);
  }
});

test("text/plain 经完整标签裁决仍不生成元素", () => {
  const nodes = parseAnnotationXmlTag(
    '<annotation-xml encoding="text/plain">',
    "<script>alert(1)</script>",
  );
  assert.deepEqual(elementNames(nodes), []);
  assert.equal(nodes[0].type, "text");
});

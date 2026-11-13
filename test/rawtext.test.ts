import assert from "node:assert/strict";
import test from "node:test";

import {
  RAW_TEXT_ELEMENTS,
  adjudicateReentry,
  analyzeRawtextPayload,
  compareScriptingModes,
  compareElementTrees,
  comparePayloadParents,
  detectMutationXss,
  parseFragment,
  serialize,
  toElementTree,
  type ElementNode,
  type TextNode,
} from "../index.ts";

const ESCAPED_IMAGE = "&lt;img src=x onerror=alert(1)&gt;";
const DECODED_IMAGE = "<img src=x onerror=alert(1)>";

function onlyRoot(source: string): ElementNode {
  const fragment = parseFragment(source);
  assert.equal(fragment.children.length, 1);
  const root = fragment.children[0];
  assert.equal(root.type, "element");
  return root as ElementNode;
}

function onlyText(element: ElementNode): TextNode {
  assert.equal(element.children.length, 1);
  const text = element.children[0];
  assert.equal(text.type, "text");
  return text as TextNode;
}

for (const parent of RAW_TEXT_ELEMENTS) {
  test(`${parent} keeps an escaped start tag as text`, () => {
    const root = onlyRoot(`<${parent}>${ESCAPED_IMAGE}</${parent}>`);
    assert.equal(root.tagName, parent);

    const text = onlyText(root);
    assert.equal(text.value, ESCAPED_IMAGE);
    assert.equal(root.children.some((node) => node.type === "element"), false);

    const result = adjudicateReentry(parent, ESCAPED_IMAGE);
    assert.equal(result.ok, true);
    assert.equal(result.stable, true);
    assert.deepEqual(result.addedElementNames, []);
    assert.deepEqual(
      result.afterTree,
      result.beforeTree,
    );
  });
}

test("p decodes the escaped tag to a text node without creating an element", () => {
  const root = onlyRoot(`<p>${ESCAPED_IMAGE}</p>`);
  const text = onlyText(root);

  assert.equal(root.tagName, "p");
  assert.equal(text.value, DECODED_IMAGE);
  assert.equal(root.children.some((node) => node.type === "element"), false);
  assert.equal(adjudicateReentry("p", ESCAPED_IMAGE).ok, true);
});

test("a raw parent and p have the same child element tree", () => {
  const comparison = comparePayloadParents(ESCAPED_IMAGE);

  assert.equal(comparison.leftParent, "noscript");
  assert.equal(comparison.rightParent, "p");
  assert.equal(comparison.sameChildElementTree, true);
  assert.equal(comparison.sameElementTree, false);
  assert.deepEqual(comparison.left.beforeTree, [
    { tag: "noscript", attributes: {}, children: [] },
  ]);
  assert.deepEqual(comparison.right.beforeTree, [
    { tag: "p", attributes: {}, children: [] },
  ]);
});

test("rawtext content is not tokenized even if it contains real markup", () => {
  for (const parent of RAW_TEXT_ELEMENTS) {
    const root = onlyRoot(`<${parent}>${DECODED_IMAGE}</${parent}>`);
    const text = onlyText(root);

    assert.equal(text.value, DECODED_IMAGE);
    assert.equal(root.children.some((node) => node.type === "element"), false);
    assert.equal(adjudicateReentry(parent, DECODED_IMAGE).ok, true);
  }
});

test("only the matching rawtext end tag closes the raw region", () => {
  const fragment = parseFragment(
    "<script>&lt;/style&gt;&lt;/script&gt;&lt;img&gt;</script><p>after</p>",
  );
  const script = fragment.children[0] as ElementNode;
  const paragraph = fragment.children[1] as ElementNode;

  assert.equal(script.tagName, "script");
  assert.equal(
    onlyText(script).value,
    "&lt;/style&gt;&lt;/script&gt;&lt;img&gt;",
  );
  assert.equal(paragraph.tagName, "p");
  assert.equal(onlyText(paragraph).value, "after");
});

test("markup after a closed rawtext element is parsed normally", () => {
  const fragment = parseFragment(
    "<noscript>&lt;img src=x&gt;</noscript><img src=y>",
  );
  assert.equal(fragment.children.length, 2);

  const noscript = fragment.children[0] as ElementNode;
  const image = fragment.children[1] as ElementNode;
  assert.equal(noscript.tagName, "noscript");
  assert.equal(onlyText(noscript).value, "&lt;img src=x&gt;");
  assert.equal(image.tagName, "img");
  assert.equal(image.children.length, 0);
});

test("serialization and re-parsing are stable for every parent", () => {
  for (const parent of [...RAW_TEXT_ELEMENTS, "p"]) {
    const source = `<${parent}>${ESCAPED_IMAGE}</${parent}>`;
    const first = parseFragment(source);
    const serialized = serialize(first);
    const second = parseFragment(serialized);

    assert.equal(
      compareElementTrees(toElementTree(first), toElementTree(second)),
      true,
    );
    assert.equal(serialize(second), serialized);
  }
});

test("analysis covers all five rawtext parents", () => {
  const report = analyzeRawtextPayload(ESCAPED_IMAGE);
  assert.deepEqual(
    report.map((item) => item.parent),
    [...RAW_TEXT_ELEMENTS],
  );
  assert.equal(report.every((item) => item.ok && item.stable), true);
});

test("scripting-mode differential exposes a new element", () => {
  const payload =
    '<noscript><p title="</noscript><img src=x onerror=alert(1)>">';
  const comparison = compareScriptingModes(payload);

  assert.equal(comparison.ok, false);
  assert.deepEqual(comparison.addedElementNames, ["img"]);
  assert.equal(comparison.sanitizer.querySelector("img"), null);
  assert.equal(comparison.browser.querySelector("img")?.tagName, "img");
  assert.equal(detectMutationXss(payload), true);
});

test("escaped rawtext payloads have no scripting-mode mutation", () => {
  for (const parent of RAW_TEXT_ELEMENTS) {
    const source = `<${parent}>${ESCAPED_IMAGE}</${parent}>`;
    assert.equal(detectMutationXss(source), false);
  }
  assert.equal(detectMutationXss(`<p>${ESCAPED_IMAGE}</p>`), false);
});

test("document parsing keeps rawtext children as text", () => {
  const fragment = parseFragment(
    "<!DOCTYPE html><html><head><title>&lt;img src=x&gt;</title></head>" +
      "<body><noscript>&lt;b&gt;no&lt;/b&gt;</noscript></body></html>",
  );

  assert.equal(fragment.children.length, 2);
  assert.equal(fragment.children[0].type, "doctype");
  const html = fragment.children[1] as ElementNode;
  const head = html.children[0] as ElementNode;
  const body = html.children[1] as ElementNode;
  const title = head.children[0] as ElementNode;
  const noscript = body.children[0] as ElementNode;

  assert.equal(onlyText(title).value, "&lt;img src=x&gt;");
  assert.equal(onlyText(noscript).value, "&lt;b&gt;no&lt;/b&gt;");
});

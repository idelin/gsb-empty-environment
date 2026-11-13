import assert from "node:assert/strict";
import test from "node:test";
import { rewriteDocument } from "../src/pipeline.ts";

test("pipeline adds rel=noopener to _blank anchors", () => {
  const html = '<a href="https://evil.example" target="_blank">go</a>';
  assert.equal(
    rewriteDocument(html),
    '<a href="https://evil.example" target="_blank" rel="noopener">go</a>',
  );
});

test("pipeline merges noopener into existing rel", () => {
  const html = '<a target="_blank" rel="nofollow">go</a>';
  assert.equal(rewriteDocument(html), '<a target="_blank" rel="nofollow noopener">go</a>');
});

test("pipeline leaves anchors that already have noopener unchanged", () => {
  const html = '<a target="_blank" rel="noopener">go</a>';
  assert.equal(rewriteDocument(html), html);
});

test("pipeline leaves non-blank anchors unchanged", () => {
  const html = '<a target="docs" href="/x">go</a>';
  assert.equal(rewriteDocument(html), html);
});

test("pipeline adds noopener to window.open third parameter", () => {
  const js = "window.open('https://evil.example', '_blank', 'width=600');";
  assert.equal(
    rewriteDocument(js),
    'window.open("https://evil.example", "_blank", "width=600,noopener");',
  );
});

test("pipeline adds a features argument when window.open has none", () => {
  const js = "window.open('https://evil.example', '_blank');";
  assert.equal(
    rewriteDocument(js),
    'window.open("https://evil.example", "_blank", "noopener");',
  );
});

test("pipeline severs named windows by default", () => {
  const js = "window.open('/docs', 'docs');";
  assert.equal(rewriteDocument(js), 'window.open("/docs", "docs", "noopener");');
});

test("pipeline honors keepOpener marker for named windows", () => {
  const js = "window.open('/docs', 'docs');";
  assert.equal(rewriteDocument(js, { keepOpener: true }), js);
});

test("pipeline leaves dynamic window.open calls untouched", () => {
  const js = "window.open(url, target, features);";
  assert.equal(rewriteDocument(js), js);
});

test("pipeline rewrites a full document with anchors and scripts", () => {
  const doc = [
    '<a href="/a" target="_blank">a</a>',
    "<script>",
    "const w = window.open('/b', '_blank', 'height=400');",
    "</script>",
  ].join("\n");
  const out = rewriteDocument(doc);
  assert.match(out, /<a href="\/a" target="_blank" rel="noopener">/);
  assert.match(out, /window\.open\("\/b", "_blank", "height=400,noopener"\)/);
});

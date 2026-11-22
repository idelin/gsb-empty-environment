import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import {
  SvgLimitError,
  SvgSanitizationError,
  SvgSanitizer,
  sanitizeSvg,
  sanitizeSvgWithReport,
} from "../src/index.ts";

const fixture = (name: string): string =>
  readFileSync(new URL(`./fixtures/${name}`, import.meta.url), "utf8");

test("removes active content, event handlers, and dangerous containers", () => {
  const result = sanitizeSvgWithReport(fixture("malicious-script-event.svg"));
  const output = result.svg;

  assert.doesNotMatch(output, /<script\b/i);
  assert.doesNotMatch(output, /<foreignObject\b/i);
  assert.doesNotMatch(output, /<animate\b/i);
  assert.doesNotMatch(output, /<set\b/i);
  assert.doesNotMatch(output, /<image\b/i);
  assert.doesNotMatch(output, /\son[a-zA-Z]+\s*=/);
  assert.doesNotMatch(output, /javascript:/i);
  assert.match(output, />safe label</);
  assert.match(output, /<rect[^>]*fill="#eef"/);
  assert.ok(result.issues.some((issue) => issue.code === "ELEMENT_REMOVED"));
  assert.equal(sanitizeSvg(output), output);
});

test("rejects external entities and DTDs before expansion", () => {
  assert.throws(
    () => sanitizeSvg(fixture("malicious-xxe.svg")),
    (error: unknown) =>
      error instanceof SvgSanitizationError &&
      error.code === "EXTERNAL_ENTITY",
  );

  assert.throws(
    () => sanitizeSvg('<svg xmlns="http://www.w3.org/2000/svg">&remote;</svg>'),
    (error: unknown) =>
      error instanceof SvgSanitizationError &&
      error.code === "EXTERNAL_ENTITY",
  );
});

test("breaks dangling and cyclic references and confines CSS resources", () => {
  const result = sanitizeSvgWithReport(
    fixture("malicious-reference-style.svg"),
  );
  const output = result.svg;

  assert.doesNotMatch(output, /attacker\.invalid/i);
  assert.doesNotMatch(output, /href="(?:https?:|\/\/|data:)/i);
  assert.doesNotMatch(output, /javascript:/i);
  assert.doesNotMatch(output, /@import/i);
  assert.doesNotMatch(output, /@font-face/i);
  assert.doesNotMatch(output, /url\(#missing\)/);
  assert.doesNotMatch(output, /#missingEscaped/);
  assert.doesNotMatch(output, /url\(#dead\)/);
  assert.doesNotMatch(output, /image\(/i);
  assert.doesNotMatch(output, /href="#loop[AB]"/);
  assert.match(output, /href="#safePath"/);
  assert.match(output, /fill: url\(#safeGradient\)/);
  assert.match(output, /\.vector path\{fill: #0b0\}/);
  assert.match(output, /fill="url\(#safeGradient\)"/);
  assert.ok(
    result.issues.filter((issue) => issue.code === "REFERENCE_REMOVED").length >=
      4,
  );
  assert.equal(sanitizeSvg(output), output);
});

test("enforces byte, depth, and node limits", () => {
  assert.throws(
    () =>
      new SvgSanitizer({ maxDepth: 3 }).sanitize(
        '<svg xmlns="http://www.w3.org/2000/svg"><g><g><g/></g></g></svg>',
      ),
    (error: unknown) =>
      error instanceof SvgLimitError && error.code === "MAX_DEPTH_EXCEEDED",
  );

  assert.throws(
    () =>
      new SvgSanitizer({ maxNodes: 3 }).sanitize(
        '<svg xmlns="http://www.w3.org/2000/svg"><rect/><rect/><rect/></svg>',
      ),
    (error: unknown) =>
      error instanceof SvgLimitError && error.code === "MAX_NODES_EXCEEDED",
  );

  assert.throws(
    () =>
      new SvgSanitizer({ maxNodes: 2 }).sanitize(
        '<svg xmlns="http://www.w3.org/2000/svg"><script/><path/></svg>',
      ),
    (error: unknown) =>
      error instanceof SvgLimitError && error.code === "MAX_NODES_EXCEEDED",
  );

  assert.throws(
    () =>
      new SvgSanitizer({ maxBytes: 8 }).sanitize(
        '<svg xmlns="http://www.w3.org/2000/svg"/>',
      ),
    (error: unknown) =>
      error instanceof SvgLimitError && error.code === "MAX_BYTES_EXCEEDED",
  );

  assert.throws(
    () => sanitizeSvg('<svg xmlns="http://www.w3.org/2000/svg">\u0001</svg>'),
    (error: unknown) =>
      error instanceof SvgSanitizationError && error.code === "INVALID_XML",
  );
});

test("preserves safe drawing, local references, and renderable styles", () => {
  const result = sanitizeSvgWithReport(fixture("normal.svg"));
  const output = result.svg;

  for (const expected of [
    'id="sky"',
    'id="glow"',
    'id="cardClip"',
    'id="dot"',
    'id="arrow"',
    'id="soft"',
    'href="#dot"',
    'clip-path="url(#cardClip)"',
    'marker-end="url(#arrow)"',
    "fill: url(#sky)",
    "filter: url(#soft)",
    "<text",
    "Rendering is intact",
  ]) {
    assert.ok(output.includes(expected), `missing ${expected}`);
  }

  assert.equal(result.issues.length, 0);
  assert.equal(sanitizeSvg(output), output);
});

test("local references may point forward but never into removed subtrees", () => {
  const output = sanitizeSvg(`
    <svg xmlns="http://www.w3.org/2000/svg">
      <use href="#later"/>
      <script id="bad">alert(1)</script>
      <rect id="later" width="4" height="4"/>
      <use href="#bad"/>
    </svg>
  `);

  assert.match(output, /href="#later"/);
  assert.doesNotMatch(output, /href="#bad"/);
});

test("accepts a BOM and does not treat comment text as a DTD", () => {
  const output = sanitizeSvg(
    '\uFEFF<!-- <!DOCTYPE is only comment text> --><svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>',
  );

  assert.match(output, /^<svg\b/);
  assert.match(output, /<rect width="1" height="1"\/>/);
  assert.doesNotMatch(output, /DOCTYPE/);
});

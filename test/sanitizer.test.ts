import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  isEventAttribute,
  isSafeUrl,
  runSanitizationPipeline,
  sanitizeDocument,
  sanitizeOne,
} from "../index.ts";

describe("event attributes inside and outside foreignObject", () => {
  const fixture = `
    <div onclick="alert('outer')" class="wrap">
      <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 200">
        <foreignObject width="100" height="100">
          <p onmouseover="alert('inner')" class="note">hello</p>
          <a href="javascript:alert(1)" onclick="steal()">link</a>
          <script>alert(1)</script>
        </foreignObject>
      </svg>
    </div>
  `;

  it("strips the event attribute outside foreignObject", () => {
    const result = sanitizeOne(fixture);
    assert.ok(!/onclick/i.test(result.html), "outer onclick must be removed");
    assert.ok(result.report.removed.some((item) => item.name === "onclick"));
  });

  it("strips the event attribute inside foreignObject", () => {
    const result = sanitizeOne(fixture);
    assert.ok(
      !/onmouseover/i.test(result.html),
      "inner onmouseover must be removed",
    );
    assert.ok(
      result.report.removed.some((item) => item.name === "onmouseover"),
    );
  });

  it("leaves no on* handler residue anywhere", () => {
    const result = sanitizeOne(fixture);
    assert.ok(!/\son[a-z]+\s*=/i.test(result.html));
  });

  it("keeps the ordinary paragraph inside foreignObject", () => {
    const result = sanitizeOne(fixture);
    assert.match(result.html, /<p class="note">hello<\/p>/);
    assert.match(result.html, /<foreignobject[^>]*>/);
  });

  it("keeps SVG geometry on the foreignObject wrapper itself", () => {
    const output = sanitizeDocument(
      '<svg><foreignobject width="100" height="50"><p>x</p></foreignobject></svg>',
    );
    assert.match(output, /<foreignobject width="100" height="50">/);
  });

  it("applies HTML element rules inside foreignObject (script dropped)", () => {
    const result = sanitizeOne(fixture);
    assert.ok(!/<script/i.test(result.html));
    assert.ok(result.report.removed.some((item) => item.tag === "script"));
  });

  it("applies the same URL restriction inside foreignObject", () => {
    const result = sanitizeOne(fixture);
    assert.ok(!/javascript:/i.test(result.html));
    assert.ok(
      result.report.removed.some(
        (item) => item.reason === "url" && item.name === "href",
      ),
    );
    // The safe anchor text remains even though the dangerous URL is gone.
    assert.match(result.html, /<a[^>]*>link<\/a>/);
  });
});

describe("URL scheme enforcement", () => {
  it("accepts http/https/mailto and relative URLs", () => {
    assert.equal(isSafeUrl("https://example.com/a"), true);
    assert.equal(isSafeUrl("/relative/path"), true);
    assert.equal(isSafeUrl("#anchor"), true);
    assert.equal(isSafeUrl("mailto:a@example.com"), true);
  });

  it("rejects javascript, vbscript and data schemes, incl. obfuscation", () => {
    assert.equal(isSafeUrl("javascript:alert(1)"), false);
    assert.equal(isSafeUrl("  JavaScript:alert(1) "), false);
    assert.equal(isSafeUrl("java\tscript:alert(1)"), false);
    assert.equal(isSafeUrl("vbscript:msgbox(1)"), false);
    assert.equal(isSafeUrl("data:text/html,x"), false);
  });
});

describe("general sanitization", () => {
  it("removes event handlers on plain HTML but keeps safe markup", () => {
    const output = sanitizeDocument(
      '<p class="x" onclick="x()">text <strong>bold</strong></p>',
    );
    assert.equal(output, '<p class="x">text <strong>bold</strong></p>');
  });

  it("unwraps disallowed elements but preserves safe children", () => {
    const output = sanitizeDocument("<div><center>hi</center></div>");
    assert.equal(output, "<div>hi</div>");
  });

  it("keeps safe svg structure and drops handlers on svg elements", () => {
    const output = sanitizeDocument(
      '<svg viewBox="0 0 10 10" onload="x()"><circle cx="1" cy="1" r="1"/></svg>',
    );
    assert.match(output, /viewBox="0 0 10 10"/);
    assert.match(output, /<circle cx="1" cy="1" r="1"\/>/);
    assert.ok(!/onload/i.test(output));
  });

  it("detects event attributes via the helper", () => {
    assert.equal(isEventAttribute("onclick"), true);
    assert.equal(isEventAttribute("on"), false);
    assert.equal(isEventAttribute("class"), false);
  });
});

describe("document pipeline", () => {
  it("processes multiple documents and reports each verdict", () => {
    const results = runSanitizationPipeline([
      { id: "a", html: '<div onclick="1()">A</div>' },
      {
        id: "b",
        html: "<svg><foreignobject><p oncut=\"1()\">B</p></foreignobject></svg>",
      },
    ]);
    assert.equal(results.length, 2);
    assert.deepEqual(results.map((result) => result.id), ["a", "b"]);
    assert.ok(results.every((result) => !/\son[a-z]+=/i.test(result.html)));
    assert.ok(results[0].report.removed.length >= 1);
    assert.ok(results[1].report.removed.some((item) => item.name === "oncut"));
    assert.match(results[1].html, /<p>B<\/p>/);
  });
});

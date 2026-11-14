import assert from "node:assert/strict";
import test from "node:test";

import sanitizeMarkdownDefault, {
  isSafeUrl,
  parseMarkdown,
  sanitizeHtml,
  sanitizeMarkdown,
  sanitizeUrl,
} from "../index.ts";

function attributeValues(html: string): string[] {
  const values: string[] = [];
  const pattern = /\b(?:href|src)="([^"]*)"/gi;
  let match: RegExpExecArray | null;
  while ((match = pattern.exec(html)) !== null) values.push(match[1]);
  return values;
}

function assertNoExecutableMarkup(html: string): void {
  assert.doesNotMatch(html, /<\s*script\b/i);
  assert.doesNotMatch(html, /<\s*(?:svg|math|iframe|object|embed|template|style)\b/i);
  assert.doesNotMatch(html, /\son[a-z]+\s*=/i);
  assert.doesNotMatch(html, /\bstyle\s*=/i);

  for (const value of attributeValues(html)) {
    assert.equal(isSafeUrl(value), true, `unsafe URL survived: ${value}`);
    const decoded = value.replace(/&amp;/g, "&").toLowerCase();
    assert.doesNotMatch(
      decoded,
      /^(?:javascript|data|vbscript)\s*:/,
      `script-capable URL survived: ${value}`,
    );
  }
}

test("rejects script-capable Markdown link protocols", () => {
  const payloads = [
    "[x](javascript:alert(1))",
    "[x](JaVaScRiPt:alert(1))",
    "[x](JAVASCRIPT:alert(1))",
    "[x](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)",
    "[x](DATA:text/html,<script>alert(1)</script>)",
    "[x](vbscript:msgbox(1))",
    "[x](VBScript:msgbox(1))",
    "[x](livescript:alert(1))",
    "[x](file:///etc/passwd)",
    "[x](blob:https://example.com/id)",
    "[x](mailto:user@example.com)",
  ];

  for (const payload of payloads) {
    const output = sanitizeMarkdown(payload);
    assertNoExecutableMarkup(output);
    assert.doesNotMatch(output, /<a\b[^>]*\b(?:href|src)\s*=/i);
  }
});

test("rejects entity-encoded and percent-encoded scheme bypasses", () => {
  const payloads = [
    "[x](&#106;avascript:alert(1))",
    "[x](&#x6A;avascript:alert(1))",
    "[x](&#x6a;avascript&#x3a;alert(1))",
    "[x](javascript&#58;alert(1))",
    "[x](javascript&colon;alert(1))",
    "[x](java&#x73;cript:alert(1))",
    "[x](java&#115;cript:alert(1))",
    "[x](&amp;#x6a;avascript:alert(1))",
    "[x](&amp;amp;#x6a;avascript:alert(1))",
    "[x](%6aavascript:alert(1))",
    "[x](%6a%61%76%61%73%63%72%69%70%74%3aalert(1))",
    "[x](java%73cript%3Aalert(1))",
    "[x](d%61ta:text/html,<script>alert(1)</script>)",
    "[x](vb%73cript:msgbox(1))",
    "[x](&Tab;javascript:alert(1))",
    "[x](java&Tab;script:alert(1))",
    "[x](java&NewLine;script:alert(1))",
    "[x](java%09script:alert(1))",
    "[x](java%0Ascript:alert(1))",
    "[x](java%0Dscript:alert(1))",
    "[x](java%00script:alert(1))",
    "[x](java\u0000script:alert(1))",
    "[x](java\u200bscript:alert(1))",
  ];

  for (const payload of payloads) {
    const output = sanitizeMarkdown(payload);
    assertNoExecutableMarkup(output);
    assert.doesNotMatch(output, /<a\b[^>]*\b(?:href|src)\s*=/i, payload);
  }
});

test("normalizes URLs before deciding and keeps safe absolute links", () => {
  assert.equal(sanitizeUrl("https://EXAMPLE.com/a b"), "https://example.com/a%20b");
  assert.equal(sanitizeUrl("HTTP://example.com/x"), "http://example.com/x");
  assert.equal(
    sanitizeMarkdown("[docs](https://example.com/a_(b)?x=1&y=2 \"Docs\")"),
    "<p><a href=\"https://example.com/a_(b)?x=1&amp;y=2\" title=\"Docs\">docs</a></p>",
  );

  const html = sanitizeMarkdown("[docs](https://example.com/a?x=1&y=2)");
  assert.match(html, /href="https:\/\/example\.com\/a\?x=1&amp;y=2"/);
  assertNoExecutableMarkup(html);
});

test("accepts ordinary relative links and images", () => {
  const cases = [
    ["[x](/docs/a)", "href=\"/docs/a\""],
    ["[x](./a.md)", "href=\"./a.md\""],
    ["[x](../a.md)", "href=\"../a.md\""],
    ["[x](guide/intro)", "href=\"guide/intro\""],
    ["[x](#section)", "href=\"#section\""],
    ["[x](?q=1)", "href=\"?q=1\""],
    ["[x](//cdn.example.com/lib.js)", "href=\"//cdn.example.com/lib.js\""],
    ["![x](images/a.png)", "src=\"images/a.png\""],
  ];

  for (const [markdown, expected] of cases) {
    const output = sanitizeMarkdown(markdown);
    assert.ok(output.includes(expected), `${markdown} => ${output}`);
    assertNoExecutableMarkup(output);
  }
});

test("drops unsafe Markdown images and keeps their alt text", () => {
  const output = sanitizeMarkdown("![diagram](javascript:alert(1))");
  assert.equal(output, "<p>diagram</p>");
  assert.doesNotMatch(output, /<img\b/i);

  const dataOutput = sanitizeMarkdown("![diagram](data:image/svg+xml;base64,PHN2Zz4=)");
  assert.equal(dataOutput, "<p>diagram</p>");
  assert.doesNotMatch(dataOutput, /<img\b/i);
});

test("sanitizes raw HTML through the tag and attribute whitelist", () => {
  const cases = [
    ["<script>alert(1)</script>", ""],
    ["<style>body{background:url(javascript:alert(1))}</style>", ""],
    ["<svg><script>alert(1)</script></svg>", ""],
    ["<iframe src=\"javascript:alert(1)\"></iframe>", ""],
    ["<img src=x onerror=alert(1)>", "<img src=\"x\">"],
    ["<div><strong onclick=x>ok</strong></div>", "<strong>ok</strong>"],
    ["<a href=\"javascript:alert(1)\">ok</a>", "<a>ok</a>"],
    ["<a href=\"&#x6a;avascript:alert(1)\">ok</a>", "<a>ok</a>"],
    ["<a href=\"java&Tab;script:alert(1)\">ok</a>", "<a>ok</a>"],
    ["<a href=\"/safe\" target=\"_blank\" rel=\"opener\">ok</a>", "<a href=\"/safe\">ok</a>"],
    ["<!-- <script>alert(1)</script> -->ok", "ok"],
  ];

  for (const [input, expected] of cases) {
    assert.equal(sanitizeHtml(input), expected, input);
  }
});

test("handles comments, malformed tags, and nested tags consistently", () => {
  const outputs = [
    sanitizeMarkdown("before <!-- <script>alert(1)</script> --> after"),
    sanitizeMarkdown("before <script>alert(1)</script> after"),
    sanitizeMarkdown("<div><a href=javascript:alert(1)><strong>nested</strong></a></div>"),
    sanitizeMarkdown("**<a href=\"javascript:alert(1)\">bold</a>**"),
    sanitizeMarkdown("<a href=\"javascript:alert(1)\"><img src=\"data:text/html,x\"></a>"),
    sanitizeMarkdown("<scr<script>ipt>alert(1)</scr</script>ipt>"),
  ];

  for (const output of outputs) assertNoExecutableMarkup(output);
  assert.match(outputs[3], /<strong><a>bold<\/a><\/strong>/);
});

test("escapes text and attributes completely", () => {
  assert.equal(
    sanitizeMarkdown("text & < > \" '"),
    "<p>text &amp; &lt; &gt; \" '</p>",
  );
  assert.equal(
    sanitizeMarkdown("[x](https://example.com/?a=1&b=2 \"a\\\"b\")"),
    "<p><a href=\"https://example.com/?a=1&amp;b=2\" title=\"a&quot;b\">x</a></p>",
  );
  assert.equal(sanitizeHtml("<p title=\"&quot;&lt;&gt;&amp;\">x</p>"), "<p title=\"&quot;&lt;&gt;&amp;\">x</p>");
  assert.equal(
    sanitizeMarkdown("<p title=\"x\" onmouseover=\"alert(1)\">x</p>"),
    "<p title=\"x\">x</p>",
  );
});

test("parseMarkdown returns an AST with sanitized link destinations", () => {
  const document = parseMarkdown("[safe](https://example.com) [bad](javascript:alert(1))");
  assert.equal(document.type, "root");
  const paragraph = document.children[0];
  assert.equal(paragraph.type, "paragraph");
  if (paragraph.type !== "paragraph") return;

  const links = paragraph.children.filter((node) => node.type === "link");
  assert.equal(links.length, 2);
  assert.equal(links[0].type === "link" ? links[0].href : undefined, "https://example.com/");
  assert.equal(links[1].type === "link" ? links[1].href : undefined, null);
});

test("default export matches the named Markdown sanitizer", () => {
  const payload = "[x](javascript:alert(1))";
  assert.equal(sanitizeMarkdownDefault(payload), sanitizeMarkdown(payload));
});

import assert from "node:assert/strict";
import test from "node:test";

import { sanitizeSvg } from "./index.ts";

function sanitize(input: string): string {
  const output = sanitizeSvg(input);
  assert.equal(sanitizeSvg(output), output, "sanitization must be idempotent");
  return output;
}

test("keeps a normal icon and its local paint server", () => {
  const output = sanitize(`<?xml version="1.0" encoding="UTF-8"?>
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">
      <title>Checkmark</title>
      <defs>
        <linearGradient id="gradient" x1="0" x2="1">
          <stop offset="0" stop-color="#34d399"/>
          <stop offset="1" stop-color="#059669"/>
        </linearGradient>
      </defs>
      <path d="M4 12.5 9.5 18 20 6.5" fill="none"
        stroke="url(#gradient)" stroke-width="2" stroke-linecap="round"/>
      <path d="M4 12.5 9.5 18 20 6.5" style="fill-rule:evenodd;opacity:0.9"/>
    </svg>`);

  assert.match(output, /^<svg xmlns="http:\/\/www\.w3\.org\/2000\/svg"/);
  assert.match(output, /<title>Checkmark<\/title>/);
  assert.match(output, /<linearGradient id="gradient" x1="0" x2="1">/);
  assert.match(output, /stroke="url\(#gradient\)"/);
  assert.match(output, /style="fill-rule:evenodd;opacity:0\.9"/);
  assert.doesNotMatch(output, /xml version/);
});

test("removes script, foreignObject, and their contents", () => {
  const output = sanitize(`<svg>
    <script>alert(1)</script>
    <SCRIPT type="text/javascript"><![CDATA[alert(2)]]></SCRIPT>
    <script>if ("<script>") alert(2)</script>
    <foreignObject><iframe src="javascript:alert(3)"></iframe></foreignObject>
    <foreignObject><foreignObject/></foreignObject>
    <x:foreignObject xmlns:x="urn:evil"><img src=x onerror=alert(4)></x:foreignObject>
    <path d="M0 0h10v10H0z"/>
  </svg>`);

  assert.doesNotMatch(output, /script/i);
  assert.doesNotMatch(output, /foreignObject/i);
  assert.doesNotMatch(output, /iframe|img|alert/i);
  assert.match(output, /<path d="M0 0h10v10H0z"\/>/);
});

test("removes event attributes including namespaced and mixed-case forms", () => {
  const output = sanitize(`<svg onload="alert(1)" OnClick="alert(2)" x:onload="alert(3)">
    <path d="M0 0" ONMOUSEOVER="alert(4)" x:onbegin="alert(5)"/>
  </svg>`);

  assert.doesNotMatch(output, /\bon[a-z]*\s*=/i);
  assert.doesNotMatch(output, /alert/i);
});

test("only accepts fragment URLs and rejects script/data schemes after entity decoding", () => {
  const output = sanitize(`<svg>
    <use href="javascript:alert(1)"/>
    <use href="JaVaScRiPt:alert(2)"/>
    <use href="java&#x0a;script:alert(3)"/>
    <use xlink:href="&#x6a;avascript:alert(4)"/>
    <use href="&#X6A;avascript:alert(5)"/>
    <use href="java&Tab;script:alert(6)"/>
    <use href="java&NewLine;script:alert(7)"/>
    <use href="data:image/svg+xml,<svg onload=alert(5)>#icon"/>
    <use href="&#x23;safe"/>
    <use xlink:href="#other"/>
  </svg>`);

  assert.doesNotMatch(output, /javascript|data:|alert/i);
  assert.match(output, /<use href="#safe"\/>/);
  assert.match(output, /<use href="#other"\/>/);
  assert.doesNotMatch(output, /xlink:/);
});

test("is idempotent after removing nested disallowed elements", () => {
  const output = sanitize(`<svg>
    <g id="group">
      <path d="M0 0"><animate attributeName="d" to="javascript:alert(1)"/></path>
    </g>
  </svg>`);

  assert.equal(output, '<svg xmlns="http://www.w3.org/2000/svg"><g id="group"><path d="M0 0"/></g></svg>');
});

test("rejects all url() and @import in style attributes", () => {
  const output = sanitize(`<svg>
    <path d="M0 0" STYLE="fill:red;background:url(javascript:alert(1))"/>
    <path d="M0 0" style="fill:red;background-image:u/**\\72l(#secret)"/>
    <path d="M0 0" style="fill:red;@\\69mport 'https://evil.example/x.css'"/>
    <path d="M0 0" style="fill:image('//evil.example/icon.svg')"/>
    <path d="M0 0" style="fill:red;fill-opacity:.5"/>
  </svg>`);

  assert.doesNotMatch(
    output,
    /url\(|@import|background|javascript|secret|image\(|evil\.example/i,
  );
  assert.match(output, /style="fill:red;fill-opacity:\.5"/);
});

test("allows only local url() references in paint and functional attributes", () => {
  const output = sanitize(`<svg>
    <defs>
      <linearGradient id="safe"><stop offset="0" stop-color="red"/></linearGradient>
      <clipPath id="clip"><rect width="10" height="10"/></clipPath>
      <filter id="blur"><feGaussianBlur stdDeviation="1"/></filter>
    </defs>
    <path d="M0 0" fill="url(&#35;safe)" clip-path="url(#clip)" filter="url(#blur)"/>
    <path d="M0 0" fill="url(javascript:alert(1))"/>
    <path d="M0 0" fill="url(data:image/svg+xml,<svg/>#x)"/>
    <path d="M0 0" clip-path="javascript:alert(2)"/>
  </svg>`);

  assert.match(output, /fill="url\(#safe\)"/);
  assert.match(output, /clip-path="url\(#clip\)"/);
  assert.match(output, /filter="url\(#blur\)"/);
  assert.doesNotMatch(output, /javascript|data:/i);
});

test("checks CSS after removing XML-invalid characters that would be stripped from output", () => {
  const nul = "\u0000";
  const unpairedSurrogate = "\ud800";
  const output = sanitize(
    `<svg>
      <path style="fill:u${nul}rl(blob:https://evil.example/a)"/>
      <path fill="u${unpairedSurrogate}rl(blob:https://evil.example/b)"/>
      <path fill="blob:https://evil.example/c"/>
      <path style="fill:blob:https://evil.example/d"/>
    </svg>`,
  );

  assert.doesNotMatch(output, /url\(|blob:/i);
});

test("normalizes case and namespaces without allowing namespaced dangerous elements", () => {
  const output = sanitize(`<svg:svg xmlns:svg="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">
    <svg:PATH D="M0 0h10" XLINK:HREF="#ignored-here"/>
    <x:script xmlns:x="urn:evil">alert(1)</x:script>
    <svg:foreignObject><svg:script>alert(2)</svg:script></svg:foreignObject>
    <svg:path d="M0 0" xlink:href="#ok"/>
  </svg:svg>`);

  assert.match(output, /^<svg xmlns="http:\/\/www\.w3\.org\/2000\/svg"/);
  assert.match(output, /<path d="M0 0h10"\/>/);
  assert.doesNotMatch(output, /script|foreignObject|xmlns:svg|xmlns:xlink/i);
});

test("rejects malformed documents and non-svg roots", () => {
  assert.throws(() => sanitizeSvg("<svg><g></svg>"), /Mismatched closing tag/);
  assert.throws(
    () => sanitizeSvg("<html><body>not an icon</body></html>"),
    /root element must be <svg>/,
  );
});

test("tolerates HTML-style unclosed vector payloads without keeping them", () => {
  const onload = sanitize("<svg onload=alert(1)>");
  const script = sanitize("<svg><script>alert(1)");
  const iframe = sanitize('<svg><iframe src="javascript:alert(1)">');

  assert.doesNotMatch(onload, /onload|alert/i);
  assert.doesNotMatch(script, /script|alert/i);
  assert.doesNotMatch(iframe, /iframe|javascript|alert/i);
});

import test from "node:test";
import assert from "node:assert/strict";
import {
  InvalidMatchPatternError,
  contentScriptsMatching,
  isWebOrigin,
  isValidMatchPattern,
  matchAnyPattern,
  matchPattern,
  matchesPatternSet,
  originOf,
  parseMatchPattern,
  resourceGlobMatches,
} from "../src/match-pattern.ts";

test("parses valid Chrome match patterns", () => {
  const pattern = parseMatchPattern("https://*.example.com/path/*");
  assert.equal(pattern.scheme, "https");
  assert.equal(pattern.hostKind, "suffix");
  assert.equal(pattern.hostSuffix, "example.com");
  assert.equal(pattern.path, "/path/*");

  assert.equal(parseMatchPattern("<all_urls>").allUrls, true);
  assert.deepEqual(parseMatchPattern("*://*/*").effectiveSchemes, ["http", "https"]);
  assert.equal(parseMatchPattern("file:///*").hostKind, "empty");
  assert.equal(isValidMatchPattern("urn://example.com/*"), true);
});

test("rejects invalid or non-Chrome match patterns", () => {
  const invalid = [
    "",
    "https://example.com",
    "example.com/*",
    "gopher://example.com/*",
    "chrome-extension://abcdef/*",
    "https://example.com:8080/*",
    "https://foo*bar.example.com/*",
    "https://*foo.example.com/*",
    "file://example.com/*",
    "<ALL_URLS>",
  ];
  for (const pattern of invalid) {
    assert.equal(isValidMatchPattern(pattern), false, pattern);
    assert.throws(() => parseMatchPattern(pattern), InvalidMatchPatternError, pattern);
  }
});

test("<all_urls> covers the permitted scheme set and excludes non-permitted schemes", () => {
  const allowed = [
    "http://example.com/a",
    "https://example.com/a",
    "ftp://example.com/a",
    "file:///tmp/a.txt",
    "ws://example.com/socket",
    "wss://example.com/socket",
    "urn:isbn:9780000000000",
  ];
  for (const url of allowed) assert.equal(matchPattern("<all_urls>", url), true, url);

  for (const url of [
    "data:text/html,<p>hi</p>",
    "blob:https://example.com/123",
    "chrome-extension://abcdef/page.html",
    "about:blank",
    "javascript:alert(1)",
  ]) {
    assert.equal(matchPattern("<all_urls>", url), false, url);
  }
});

test("scheme wildcard only matches http and https", () => {
  assert.equal(matchPattern("*://*/*", "http://example.com/a"), true);
  assert.equal(matchPattern("*://*/*", "https://example.com/a"), true);
  assert.equal(matchPattern("*://*/*", "ftp://example.com/a"), false);
  assert.equal(matchPattern("*://*/*", "file:///tmp/a"), false);
  assert.equal(matchPattern("*://*/*", "ws://example.com/a"), false);
});

test("wildcard subdomain matches apex and subdomains but not suffix confusion", () => {
  assert.equal(matchPattern("https://*.example.com/*", "https://example.com/a"), true);
  assert.equal(matchPattern("https://*.example.com/*", "https://www.example.com/a"), true);
  assert.equal(matchPattern("https://*.example.com/*", "https://a.b.example.com/a"), true);
  assert.equal(matchPattern("https://*.example.com/*", "https://example.com.evil/a"), false);
  assert.equal(matchPattern("https://*.example.com/*", "https://notexample.com/a"), false);
  assert.equal(matchPattern("https://*.example.com/*", "http://www.example.com/a"), false);
});

test("exact host is exact, and ports do not affect host matching", () => {
  assert.equal(matchPattern("https://example.com/*", "https://example.com:8443/a"), true);
  assert.equal(matchPattern("https://example.com/*", "https://www.example.com/a"), false);
  assert.equal(matchPattern("https://example.com/*", "https://example.com./a"), true);
});

test("path matching follows Chrome URLPattern and ignores query/fragment", () => {
  assert.equal(matchPattern("https://example.com/*", "https://example.com/a?x=1#frag"), true);
  assert.equal(matchPattern("https://example.com/foo", "https://example.com/foo"), true);
  assert.equal(matchPattern("https://example.com/foo", "https://example.com/foo?x=1"), true);
  assert.equal(matchPattern("https://example.com/foo*", "https://example.com/foo?x=1"), true);
  assert.equal(matchPattern("https://example.com/foo", "https://example.com/bar?x=1"), false);
});

test("match sets implement include minus exclude", () => {
  const target = "https://app.example.com/admin";
  assert.equal(
    matchesPatternSet(target, ["*://*/*"], ["*://*.example.com/admin*", "https://app.example.com/*"]),
    false,
  );
  assert.equal(matchesPatternSet(target, ["https://app.example.com/*"], ["https://app.example.com/admin"]), false);
  assert.equal(matchesPatternSet("https://other.example/x", ["https://app.example.com/*"], ["https://app.example.com/*"]), false);
  assert.equal(matchAnyPattern(["https://example.com/*", "*://*/*"], target), true);
});

test("content script matching honors exclude_matches and globs", () => {
  const manifest = {
    manifest_version: 3 as const,
    content_scripts: [
      {
        matches: ["<all_urls>"],
        exclude_matches: ["https://internal.example/*", "https://*.corp.example/*"],
        exclude_globs: ["*://example.com/private/*"],
        js: ["content.js"],
      },
    ],
  };
  assert.equal(contentScriptsMatching(manifest, "https://public.example/page").length, 1);
  assert.equal(contentScriptsMatching(manifest, "https://internal.example/page").length, 0);
  assert.equal(contentScriptsMatching(manifest, "https://app.corp.example/page").length, 0);
  assert.equal(contentScriptsMatching(manifest, "https://example.com/private/page").length, 0);
});

test("origin normalization and resource globs are strict", () => {
  assert.equal(originOf("https://example.com/path"), "https://example.com");
  assert.equal(originOf("file:///tmp/a"), "file://");
  assert.equal(isWebOrigin("https://example.com"), true);
  assert.equal(isWebOrigin("chrome-extension://abcdef"), false);
  assert.equal(resourceGlobMatches("*.html", "options.html"), true);
  assert.equal(resourceGlobMatches("images/*", "images/logo.png"), true);
  assert.equal(resourceGlobMatches("images/*", "css/logo.png"), false);
});

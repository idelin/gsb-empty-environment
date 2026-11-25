import test from "node:test";
import assert from "node:assert/strict";
import {
  compileDomainLanguage,
  dfaAccepts,
  dfaDigest,
  dfaIntersection,
  dfaIsEmpty,
  dfaSample,
  dfaSubset,
  domainSuffixLanguage,
} from "../src/index.ts";

test("domain suffix language respects DNS label boundaries", () => {
  const suffix = domainSuffixLanguage("example.com");
  assert.equal(dfaAccepts(suffix, "example.com"), true);
  assert.equal(dfaAccepts(suffix, "alice.example.com"), true);
  assert.equal(dfaAccepts(suffix, "alice.dev.example.com"), true);
  assert.equal(dfaAccepts(suffix, "badexample.com"), false);
  assert.equal(dfaAccepts(suffix, "example.com.evil.test"), false);
});

test("regular-language intersection is exact and produces a witness", () => {
  const broad = compileDomainLanguage("^(?:[a-z]+\\.)*example\\.com$");
  const narrow = compileDomainLanguage("^dev\\.[a-z]+\\.example\\.com$");
  const intersection = dfaIntersection(broad, narrow);
  assert.equal(dfaAccepts(intersection, "dev.alice.example.com"), true);
  assert.equal(dfaAccepts(intersection, "ops.alice.example.com"), false);
  assert.equal(dfaSubset(narrow, broad), true);
  assert.equal(dfaIsEmpty(intersection), false);
  assert.equal(dfaAccepts(intersection, dfaSample(intersection) ?? ""), true);
});

test("disjoint domain languages intersect to the empty language", () => {
  const example = compileDomainLanguage("(?:[a-z]+\\.)*example\\.com");
  const org = compileDomainLanguage("(?:[a-z]+\\.)*example\\.org");
  const intersection = dfaIntersection(example, org);
  assert.equal(dfaIsEmpty(intersection), true);
  assert.equal(dfaSample(intersection), null);
  assert.equal(dfaSubset(org, example), false);
});

test("equivalent regexes have the same canonical language digest", () => {
  const first = compileDomainLanguage("[a-z]+");
  const second = compileDomainLanguage("[a-z][a-z]*");
  assert.equal(dfaDigest(first), dfaDigest(second));
  assert.equal(dfaSubset(first, second), true);
  assert.equal(dfaSubset(second, first), true);
});

test("domain matching is case-insensitive after normalization", () => {
  const language = compileDomainLanguage("^alice\\.example\\.com$");
  assert.equal(dfaAccepts(language, "ALICE.EXAMPLE.COM".toLowerCase()), true);
});

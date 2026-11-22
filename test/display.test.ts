import assert from "node:assert/strict";
import test from "node:test";

import {
  checkAllowedRange,
  decideAllVersions,
  decideDisplay,
  decideForVersion,
  verifyDecision,
  verifyTableSummary,
} from "../src/index.ts";
import {
  createConfusableMatcher,
  createConfusableProgram,
  skeletonDomain,
  transcribe,
} from "../src/confusables.ts";
import { dfaFromWords, searchCandidates } from "../src/automaton.ts";
import {
  createHmacAliasVerifier,
  signAlias,
  verifyAliasSignature,
} from "../src/aliases.ts";
import { sha256Hex, hmacSha256Hex } from "../src/canonical.ts";
import { effectiveSuffix } from "../src/suffix.ts";
import { scriptPolicyHolds } from "../src/scripts.ts";
import {
  isBoundaryInsideSurrogatePair,
  scalarSpans,
  toAsciiLabel,
} from "../src/unicode.ts";
import { resolveLabels } from "../src/decide.ts";
import {
  exhaustiveWords,
  runExhaustiveChecks,
} from "../src/exhaustive.ts";
import { summarizeTables } from "../src/tables.ts";
import {
  KEY,
  LATIN,
  LATIN_GREEK,
  MIXED,
  makeAliases,
  makeConfusablesV1,
  makeDecisionRequest,
  makeScriptTable,
  makeStreamingAlternate,
  makeStreamingTable,
  makeSuffixTable,
} from "./model.ts";

test("SHA-256, HMAC and punycode match known vectors", () => {
  assert.equal(
    sha256Hex("abc"),
    "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
  );
  assert.equal(
    hmacSha256Hex("key", "The quick brown fox jumps over the lazy dog"),
    "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8",
  );
  assert.equal(toAsciiLabel("bücher"), "xn--bcher-kva");
  assert.equal(toAsciiLabel("mañana"), "xn--maana-pta");
  assert.equal(toAsciiLabel("例え"), "xn--r8jz45g");
});

test("scalar spans keep surrogate pairs intact", () => {
  const spans = scalarSpans("a𐐀b", 10);
  assert.deepEqual(spans, [
    { codePoint: 0x61, utf16Start: 10, utf16End: 11 },
    { codePoint: 0x10400, utf16Start: 11, utf16End: 13 },
    { codePoint: 0x62, utf16Start: 13, utf16End: 14 },
  ]);
  assert.equal(isBoundaryInsideSurrogatePair("a𐐀b", 2), true);
  assert.equal(isBoundaryInsideSurrogatePair("a𐐀b", 1), false);
  assert.equal(isBoundaryInsideSurrogatePair("a𐐀b", 3), false);
});

test("streaming matcher performs longest match across chunks and flushes pending", () => {
  const table = makeStreamingTable();
  assert.deepEqual(transcribe(table, "abc"), []);
  assert.deepEqual(transcribe(table, "ab"), [0x58]);
  assert.deepEqual(transcribe(table, "ac"), [0x41, 0x43]);
  assert.deepEqual(transcribe(table, "a"), [0x41]);
  assert.deepEqual(transcribe(table, "abx"), [0x58, 0x78]);

  const matcher = createConfusableMatcher(table);
  assert.deepEqual(matcher.push("a"), []);
  assert.deepEqual(matcher.pending(), [0x61]);
  assert.deepEqual(matcher.push("b"), []);
  assert.deepEqual(matcher.pending(), [0x61, 0x62]);
  assert.deepEqual(matcher.push("c"), []);
  assert.deepEqual(matcher.end(), []);

  const compact = createConfusableMatcher(table);
  assert.deepEqual(compact.push("ab"), []);
  assert.deepEqual(compact.end(), [0x58]);

  const empty = createConfusableMatcher({ version: "empty", rules: [] });
  assert.deepEqual(empty.push("\ud83d"), []);
  assert.deepEqual(empty.push("\ude00"), [0x1f600]);
  assert.deepEqual(empty.end(), []);
});

test("empty-output rules consume input without swallowing later input", () => {
  const table = makeStreamingTable();
  const matcher = createConfusableMatcher(table);
  assert.deepEqual(matcher.push("abc"), []);
  assert.deepEqual(matcher.end(), []);
  assert.deepEqual(matcher.push("a"), []);
  assert.deepEqual(matcher.end(), [0x41]);
});

test("domain skeleton streams across label separators when a rule says so", () => {
  const table = {
    version: "dot-rule",
    rules: [
      {
        consumes: [0x61, 0x2e, 0x62],
        skeleton: [0x78],
      },
    ],
  };
  assert.deepEqual(skeletonDomain(table, ["a", "b"]), [0x78]);
  assert.deepEqual(skeletonDomain(table, ["a", "c"]), [0x61, 0x2e, 0x63]);
});

test("script policy uses Script_Extensions as choice sets", () => {
  const table = makeScriptTable("v1");
  const g = 0x0261;
  const alpha = 0x03b1;
  const cyrillicA = 0x0430;
  assert.equal(scriptPolicyHolds(table, { allowed: ["Greek"] }, [g]), true);
  assert.equal(scriptPolicyHolds(table, { allowed: ["Han"] }, [g]), false);
  assert.equal(
    scriptPolicyHolds(table, { allowed: ["Latin", "Greek"] }, [g, alpha]),
    true,
  );
  assert.equal(
    scriptPolicyHolds(
      table,
      { allowed: ["Latin", "Greek", "Cyrillic"] },
      [g, cyrillicA],
    ),
    false,
  );
  assert.equal(
    scriptPolicyHolds(
      table,
      { allowed: ["Latin", "Greek", "Cyrillic"], singleScript: false },
      [g, cyrillicA],
    ),
    true,
  );
  assert.equal(
    scriptPolicyHolds(
      table,
      {
        allowed: ["Latin", "Greek"],
        singleScript: false,
        maxScripts: 1,
      },
      [g, alpha],
    ),
    true,
  );
  assert.equal(
    scriptPolicyHolds(
      table,
      {
        allowed: ["Latin", "Greek", "Cyrillic"],
        singleScript: false,
        maxScripts: 1,
      },
      [g, cyrillicA],
    ),
    false,
  );
  assert.equal(
    scriptPolicyHolds(table, { allowed: [] }, [0x21]),
    true,
  );
});

test("suffix wildcard and exception precedence", () => {
  const table = makeSuffixTable();
  const www = effectiveSuffix(["www", "ck"], table);
  assert.equal(www.suffix, "ck");
  assert.equal(www.kind, "exception");
  assert.equal(www.policy, LATIN_GREEK);

  const wildcard = effectiveSuffix(["a", "b", "ck"], table);
  assert.equal(wildcard.suffix, "b.ck");
  assert.equal(wildcard.kind, "wildcard");
  assert.equal(wildcard.policy, LATIN);

  const nested = effectiveSuffix(["a", "b", "co", "uk"], table);
  assert.equal(nested.suffix, "co.uk");
  assert.equal(nested.policy, LATIN_GREEK);

  assert.equal(effectiveSuffix(["x", "uk"], table).suffix, "uk");
  assert.equal(effectiveSuffix(["x", "com"], table).suffix, "com");

  const tie = {
    version: "tie",
    rules: [
      { suffix: "uk", kind: "wildcard" as const, policy: LATIN },
      { suffix: "co.uk", kind: "normal" as const, policy: LATIN_GREEK },
    ],
  };
  const tied = effectiveSuffix(["a", "co", "uk"], tie);
  assert.equal(tied.suffix, "co.uk");
  assert.equal(tied.policy, LATIN_GREEK);
});

test("signed aliases are verified and restricted to a table version", () => {
  const alias = signAlias(
    {
      domain: "аbc.com",
      brand: "Example",
      suffix: "com",
      tableVersion: "v1",
      keyId: "k1",
    },
    KEY,
  );
  assert.equal(verifyAliasSignature(alias, KEY), true);
  assert.equal(verifyAliasSignature(alias, "wrong-key"), false);
  const verifier = createHmacAliasVerifier({ k1: KEY });
  assert.equal(verifier(alias), true);
  assert.equal(verifier({ ...alias, brand: "Other" }), false);

  const base64Alias = signAlias(
    {
      domain: "αbc.com",
      brand: "Example",
      suffix: "com",
      keyId: "k1",
    },
    KEY,
    { signatureEncoding: "base64" },
  );
  assert.equal(base64Alias.signatureEncoding, "base64");
  assert.equal(verifyAliasSignature(base64Alias, KEY), true);
});

test("new table falls back to old version only for old-version aliases", () => {
  const request = makeDecisionRequest({ versionFallback: true });
  const decision = decideDisplay(request);
  assert.equal(decision.attempts.length, 2);
  assert.equal(decision.attempts[0].version, "v2");
  assert.equal(decision.attempts[0].ok, false);
  assert.equal(decision.attempts[1].version, "v1");
  assert.equal(decision.attempts[1].ok, true);
  assert.equal(decision.version, "v1");
  assert.equal(decision.proof.ok, true);
  assert.equal(decision.labels[0].mode, "unicode");
  assert.equal(decision.labels[0].reason, "proved");
  assert.equal(decision.labels[1].mode, "ascii");
  assert.equal(decision.attempts[0].proof.witness?.domain, "αbc.com");
  assert.ok(decision.attempts[1].proof.acceptedSameSkeleton >= 2);
  assert.deepEqual(decision.allowedRange.unicodeLabels, [0]);
  assert.deepEqual(decision.allowedRange.asciiLabels, [1]);

  const report = verifyDecision(decision, request);
  assert.equal(report.ok, true, report.errors.join("\n"));
});

test("a single version that cannot prove falls back to the label A-form", () => {
  const request = makeDecisionRequest();
  const decision = decideForVersion(request, "v2");
  assert.equal(decision.proof.ok, false);
  assert.equal(decision.labels[0].mode, "ascii");
  assert.equal(decision.labels[0].displayText, toAsciiLabel("аbc"));
  assert.equal(decision.labels[0].reason, "no-proof");
  const report = verifyDecision(decision, {
    ...request,
    versionOrder: ["v2"],
  });
  assert.equal(report.ok, true, report.errors.join("\n"));
});

test("unrestricted aliases let the new table prove the same candidate set", () => {
  const request = makeDecisionRequest({
    aliases: makeAliases(false),
  });
  const decision = decideDisplay(request);
  assert.equal(decision.version, "v2");
  assert.equal(decision.proof.ok, true);
  assert.equal(decision.labels[0].mode, "unicode");
  const report = verifyDecision(decision, request);
  assert.equal(report.ok, true, report.errors.join("\n"));
});

test("decideAllVersions keeps old and new bookmark decisions separate", () => {
  const request = makeDecisionRequest();
  const decisions = decideAllVersions(request);
  assert.deepEqual(
    decisions.map((decision) => decision.version),
    ["v2", "v1"],
  );
  assert.equal(decisions[0].proof.ok, false);
  assert.equal(decisions[0].labels[0].mode, "ascii");
  assert.equal(decisions[1].proof.ok, true);
  assert.equal(decisions[1].labels[0].mode, "unicode");
});

test("an unproven same-skeleton candidate forces ASCII fallback", () => {
  const request = makeDecisionRequest({
    aliases: [],
  });
  const decision = decideDisplay(request);
  assert.equal(decision.proof.ok, false);
  assert.equal(decision.labels[0].mode, "ascii");
  assert.equal(decision.labels[0].displayText, toAsciiLabel("аbc"));
});

test("UTF-16 spans point at the actual labels and never split surrogates", () => {
  const request = makeDecisionRequest({
    labels: ["a𐐀b", "example"],
    sourceText: "xx.a𐐀b.example",
    protectedDomain: "a𐐀b.example",
    automaton: undefined,
    aliases: [],
    versionOrder: ["v1"],
  });
  const resolved = resolveLabels(request.labels, request.sourceText);
  assert.deepEqual(
    resolved.map((label) => [label.start, label.end]),
    [
      [3, 7],
      [8, 15],
    ],
  );
  const decision = decideDisplay(request);
  assert.equal(decision.labels[0].span.start, 3);
  assert.equal(decision.labels[0].span.end, 7);
  assert.deepEqual(
    decision.labels[0].scalars.map((span) => [span.utf16Start, span.utf16End]),
    [
      [3, 4],
      [4, 6],
      [6, 7],
    ],
  );
  assert.equal(decision.labels[0].mode, "unicode");
  const report = verifyDecision(decision, request);
  assert.equal(report.ok, true, report.errors.join("\n"));

  assert.throws(() =>
    resolveLabels([{ text: "𐐀", start: 1, end: 2 }], "a𐐀"),
  );
});

test("candidate search finds an unproven witness without enumerating names", () => {
  const program = createConfusableProgram({
    version: "v1",
    rules: [{ consumes: [0x62], skeleton: [] }],
  });
  const dfa = {
    initial: 0,
    accepting: [1, 2],
    edges: [
      { from: 0, to: 1, start: 0x61, end: 0x61 },
      { from: 1, to: 2, start: 0x62, end: 0x62 },
      { from: 2, to: 2, start: 0x62, end: 0x62 },
    ],
    stateCount: 3,
  };
  const result = searchCandidates(dfa, program, [0x61], {
    maxScalars: 64,
    provenDomains: ["a"],
  });
  assert.equal(result.ok, false);
  assert.equal(result.witness?.domain, "ab");
  assert.ok(result.exploredStates < 10);
  assert.ok(result.acceptedSameSkeleton >= 1);

  const shallow = searchCandidates(dfa, program, [0x61], {
    maxScalars: 1,
    provenDomains: ["a"],
  });
  assert.equal(shallow.ok, true);
  assert.equal(shallow.acceptedSameSkeleton, 1);
});

test("candidate search proves every same-skeleton alternative", () => {
  const program = createConfusableProgram({
    version: "v1",
    rules: [{ consumes: [0x61], skeleton: [0x61] }],
  });
  const dfa = dfaFromWords(["a", "aa", "aaa"]);
  const result = searchCandidates(dfa, program, [0x61, 0x61, 0x61], {
    maxScalars: 64,
    provenDomains: ["aaa"],
  });
  assert.equal(result.ok, true);
  assert.equal(result.witness, null);
  assert.equal(result.acceptedSameSkeleton, 1);

  const missing = searchCandidates(dfa, program, [0x61, 0x61, 0x61], {
    maxScalars: 64,
    provenDomains: [],
  });
  assert.equal(missing.ok, false);
  assert.equal(missing.witness?.domain, "aaa");
});

test("candidate script filter ignores domains that fail the suffix policy", () => {
  const program = createConfusableProgram({
    version: "v1",
    rules: [{ consumes: [0x430], skeleton: [0x61] }],
  });
  const dfa = dfaFromWords(["а"]);
  const unfiltered = searchCandidates(dfa, program, [0x61], {
    provenDomains: [],
  });
  assert.equal(unfiltered.ok, false);
  assert.equal(unfiltered.witness?.domain, "а");

  const filtered = searchCandidates(dfa, program, [0x61], {
    provenDomains: [],
    isAllowedDomain: (domain) => domain !== "а",
  });
  assert.equal(filtered.ok, true);
  assert.equal(filtered.acceptedSameSkeleton, 0);

  const request = makeDecisionRequest({
    labels: ["abc", "uk"],
    sourceText: "abc.uk",
    protectedDomain: "abc.uk",
    aliases: [],
    automaton: dfaFromWords(["аbc.uk"]),
    versionOrder: ["v1"],
    filterCandidatesByScriptPolicy: true,
  });
  const filteredDecision = decideDisplay(request);
  assert.equal(filteredDecision.proof.ok, true);
  assert.equal(
    verifyDecision(filteredDecision, request).ok,
    true,
  );
  assert.equal(
    decideDisplay({
      ...request,
      filterCandidatesByScriptPolicy: false,
    }).proof.ok,
    false,
  );
});

test("checker rejects tampered digests and allowed ranges", () => {
  const request = makeDecisionRequest({ versionFallback: true });
  const decision = decideDisplay(request);
  assert.equal(checkAllowedRange(decision, request).ok, true);

  const badDigest = {
    ...decision,
    tableDigests: { ...decision.tableDigests, confusables: "deadbeef" },
  };
  const digestReport = verifyDecision(badDigest, request);
  assert.equal(digestReport.ok, false);
  assert.ok(
    digestReport.errors.some((error) => error.includes("digest")),
  );

  const badRange = {
    ...decision,
    allowedRange: { ...decision.allowedRange, unicodeLabels: [] },
  };
  const rangeReport = verifyDecision(badRange, request);
  assert.equal(rangeReport.ok, false);
  assert.ok(
    rangeReport.errors.some((error) => error.includes("allowed")),
  );

  const summary = summarizeTables(request.tables);
  assert.equal(verifyTableSummary(summary, request.tables).ok, true);
  assert.equal(
    verifyTableSummary(
      { ...summary, combinedDigest: "00" },
      request.tables,
    ).ok,
    false,
  );
});

test("candidate search clamps the candidate length cap to 64 scalars", () => {
  const program = createConfusableProgram({ version: "v", rules: [] });
  const longWord = `a${"b".repeat(64)}`;
  const dfa = dfaFromWords([longWord]);
  const target = Array.from(longWord, (character) =>
    character.codePointAt(0) as number,
  );
  const result = searchCandidates(dfa, program, target, {
    maxScalars: 65,
  });
  assert.equal(result.ok, true);
  assert.equal(result.acceptedSameSkeleton, 0);
});

test("product search agrees with brute force on a small language", () => {
  const alphabet = [0x61, 0x62, 0x63];
  const words = exhaustiveWords(alphabet, 2).map((word) =>
    String.fromCodePoint(...word),
  );
  const dfa = dfaFromWords(words);
  const program = createConfusableProgram({
    version: "v",
    rules: [
      { consumes: [0x61], skeleton: [0x78] },
      { consumes: [0x61, 0x62], skeleton: [] },
      { consumes: [0x62], skeleton: [0x79] },
    ],
  });
  const skeletonOf = (word: string) => transcribe(program, word);
  const sameScalars = (a: number[], b: number[]) =>
    a.length === b.length && a.every((value, index) => value === b[index]);

  for (const targetWord of words) {
    const target = skeletonOf(targetWord);
    const same = words.filter((word) =>
      sameScalars(skeletonOf(word), target),
    );
    const provenSets = [
      new Set<string>(),
      new Set(same),
      new Set(same.slice(1)),
    ];
    for (const proven of provenSets) {
      const expected = same.every((word) => proven.has(word));
      const result = searchCandidates(dfa, program, target, {
        maxScalars: 2,
        provenDomains: proven,
      });
      assert.equal(
        result.ok,
        expected,
        `${targetWord}: ${JSON.stringify([...proven])}`,
      );
    }
  }
});

test("exhaustive checks cover streaming, empty output, extensions, suffix exceptions and old aliases", () => {
  const report = runExhaustiveChecks({
    confusables: makeStreamingTable(),
    alternateConfusables: makeStreamingAlternate(),
    scripts: makeScriptTable("v1"),
    policies: [
      MIXED,
      LATIN,
      LATIN_GREEK,
      { allowed: ["Greek"] },
      { allowed: [] },
      {
        allowed: ["Latin", "Greek", "Cyrillic"],
        singleScript: false,
        maxScripts: 1,
      },
      {
        allowed: ["Latin", "Greek", "Cyrillic"],
        singleScript: false,
        minScripts: 2,
      },
    ],
    suffixTable: makeSuffixTable(),
    suffixCases: [
      { labels: ["www", "ck"], expectedSuffix: "ck" },
      { labels: ["a", "b", "ck"], expectedSuffix: "b.ck" },
      { labels: ["a", "b", "co", "uk"], expectedSuffix: "co.uk" },
    ],
    labelAlphabet: ["a", "b", "ck", "www", "co", "uk", "com"],
    alphabet: [0x61, 0x62, 0x63, 0x430, 0x3b1, 0x261],
    maxWordLength: 3,
    maxWords: 400,
    maxChunkings: 8,
    versionFallback: {
      request: makeDecisionRequest(),
      newVersion: "v2",
      oldVersion: "v1",
    },
  });
  const summary = report.checks
    .map((check) => `${check.name}: ${check.ok ? "ok" : check.failures.join("; ")}`)
    .join("\n");
  assert.equal(report.ok, true, summary);
  assert.ok(report.checks.length >= 5);
});

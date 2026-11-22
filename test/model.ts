import { dfaFromWords } from "../src/automaton.ts";
import { signAlias } from "../src/aliases.ts";
import {
  scriptsFromRanges,
} from "../src/scripts.ts";
import type {
  ConfusableTable,
  DisplayRequest,
  ScriptPolicy,
  ScriptTable,
  SuffixTable,
} from "../src/types.ts";

export const KEY = "test-hmac-key";

export const MIXED: ScriptPolicy = {
  allowed: ["Latin", "Greek", "Cyrillic", "Deseret"],
  singleScript: false,
};

export const LATIN: ScriptPolicy = {
  allowed: ["Latin"],
  singleScript: true,
};

export const LATIN_GREEK: ScriptPolicy = {
  allowed: ["Latin", "Greek"],
  singleScript: true,
};

export function makeScriptTable(version: string): ScriptTable {
  return scriptsFromRanges(
    version,
    [
      { start: 0x41, end: 0x5a, script: "Latin" },
      { start: 0x61, end: 0x7a, script: "Latin" },
      { start: 0x00fc, end: 0x00fc, script: "Latin" },
      {
        start: 0x0261,
        end: 0x0261,
        script: "Latin",
        extensions: ["Latin", "Greek"],
      },
      { start: 0x03b1, end: 0x03b1, script: "Greek" },
      { start: 0x0430, end: 0x0430, script: "Cyrillic" },
      { start: 0x10400, end: 0x10427, script: "Deseret" },
    ],
    { defaultScript: "Common" },
  );
}

export function makeConfusablesV1(): ConfusableTable {
  return {
    version: "v1",
    rules: [
      { consumes: [0x0430], skeleton: [0x61] },
      { consumes: [0x03b1], skeleton: [0x61] },
      { consumes: [0x0261], skeleton: [0x67] },
      { consumes: [0x00fc], skeleton: [0x75] },
    ],
  };
}

export function makeConfusablesV2(): ConfusableTable {
  return {
    version: "v2",
    rules: [
      // The new table no longer folds Cyrillic a to Latin a.
      { consumes: [0x0430], skeleton: [0x0430] },
      { consumes: [0x03b1], skeleton: [0x61] },
      { consumes: [0x0261], skeleton: [0x67] },
      { consumes: [0x00fc], skeleton: [0x75] },
    ],
  };
}

export function makeSuffixTable(): SuffixTable {
  return {
    version: "s1",
    rules: [
      { suffix: "com", policy: MIXED },
      { suffix: "co.uk", policy: LATIN_GREEK },
      { suffix: "uk", policy: LATIN },
      { suffix: "ck", kind: "wildcard", policy: LATIN },
      { suffix: "www.ck", kind: "exception", policy: LATIN_GREEK },
      { suffix: "example", policy: MIXED },
    ],
    defaultPolicy: MIXED,
  };
}

export function makeAliases(restrictToOldVersion: boolean) {
  return ["аbc.com", "αbc.com"].map((domain) =>
    signAlias(
      {
        domain,
        brand: "Example",
        suffix: "com",
        keyId: "k1",
        ...(restrictToOldVersion ? { tableVersion: "v1" } : {}),
      },
      KEY,
    ),
  );
}

export function makeTables() {
  return {
    confusables: {
      v1: makeConfusablesV1(),
      v2: makeConfusablesV2(),
    },
    scripts: {
      v1: makeScriptTable("v1"),
      v2: makeScriptTable("v2"),
    },
    suffixes: {
      s1: makeSuffixTable(),
    },
  };
}

export function makeDecisionRequest(
  overrides: Partial<DisplayRequest> = {},
): DisplayRequest {
  return {
    labels: ["аbc", "com"],
    sourceText: "аbc.com",
    protectedDomain: "abc.com",
    brand: "Example",
    versionOrder: ["v2", "v1"],
    suffixVersion: "s1",
    tables: makeTables(),
    aliases: makeAliases(true),
    automaton: dfaFromWords(["аbc.com", "αbc.com"]),
    keyring: { k1: KEY },
    now: 1000,
    ...overrides,
  };
}

export function makeStreamingTable(): ConfusableTable {
  return {
    version: "stream-1",
    rules: [
      { consumes: [0x61, 0x62, 0x63], skeleton: [] },
      { consumes: [0x61, 0x62], skeleton: [0x58] },
      { consumes: [0x61], skeleton: [0x41] },
      { consumes: [0x61], skeleton: [0x5a] },
      { consumes: [0x62], skeleton: [0x42] },
      { consumes: [0x63], skeleton: [0x43] },
    ],
  };
}

export function makeStreamingAlternate(): ConfusableTable {
  return {
    version: "stream-2",
    rules: [
      { consumes: [0x61, 0x62], skeleton: [0x59] },
      { consumes: [0x61], skeleton: [0x41] },
      { consumes: [0x62], skeleton: [0x42] },
      { consumes: [0x63], skeleton: [0x43] },
    ],
  };
}

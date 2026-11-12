import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  canUseSharedArrayBuffer,
  evaluateCrossOriginIsolation,
  isCrossOriginIsolated,
  parseStructuredFieldItem,
} from "../index.ts";

const COOP = "Cross-Origin-Opener-Policy";
const COEP = "Cross-Origin-Embedder-Policy";
const COOP_RO = "Cross-Origin-Opener-Policy-Report-Only";
const COEP_RO = "Cross-Origin-Embedder-Policy-Report-Only";

const headers = (
  coop: string | readonly string[] | undefined,
  coep: string | readonly string[] | undefined,
  extras: readonly (readonly [string, string])[] = [],
): (readonly [string, string])[] => {
  const result: (readonly [string, string])[] = [];
  if (typeof coop === "string") {
    result.push([COOP, coop]);
  } else if (coop !== undefined) {
    for (const value of coop) {
      result.push([COOP, value]);
    }
  }
  if (typeof coep === "string") {
    result.push([COEP, coep]);
  } else if (coep !== undefined) {
    for (const value of coep) {
      result.push([COEP, value]);
    }
  }
  result.push(...extras);
  return result;
};

describe("cross-origin isolation", () => {
  it("requires same-origin COOP and require-corp COEP together", () => {
    const result = evaluateCrossOriginIsolation(
      headers("same-origin", "require-corp"),
    );

    assert.equal(result.isolated, true);
    assert.equal(result.sharedArrayBuffer, true);
    assert.equal(result.coop.status, "effective");
    assert.equal(result.coep.status, "effective");
    assert.deepEqual(result.failures, []);
    assert.equal(isCrossOriginIsolated(headers("same-origin", "require-corp")), true);
    assert.equal(canUseSharedArrayBuffer(headers("same-origin", "require-corp")), true);
  });

  it("fails closed when either policy is missing", () => {
    assert.equal(evaluateCrossOriginIsolation(headers("same-origin", undefined)).isolated, false);
    assert.equal(evaluateCrossOriginIsolation(headers(undefined, "require-corp")).isolated, false);
    assert.equal(evaluateCrossOriginIsolation(undefined).isolated, false);
    assert.equal(evaluateCrossOriginIsolation([]).isolated, false);
    assert.equal(evaluateCrossOriginIsolation(null).isolated, false);
  });

  it("does not accept weaker policy tokens", () => {
    assert.equal(
      evaluateCrossOriginIsolation(
        headers("same-origin-allow-popups", "require-corp"),
      ).isolated,
      false,
    );
    assert.equal(
      evaluateCrossOriginIsolation(headers("unsafe-none", "require-corp")).isolated,
      false,
    );
    assert.equal(
      evaluateCrossOriginIsolation(headers("same-origin", "credentialless")).isolated,
      false,
    );
    assert.equal(
      evaluateCrossOriginIsolation(headers("same-origin", "unsafe-none")).isolated,
      false,
    );
  });

  it("treats tokens as case-sensitive", () => {
    const result = evaluateCrossOriginIsolation(
      headers("Same-Origin", "Require-Corp"),
    );

    assert.equal(result.isolated, false);
    assert.equal(result.coop.status, "ineffective");
    assert.equal(result.coep.status, "ineffective");
  });

  it("matches header names case-insensitively", () => {
    const result = evaluateCrossOriginIsolation([
      ["cross-origin-opener-policy", "same-origin"],
      ["CROSS-ORIGIN-EMBEDDER-POLICY", "require-corp"],
    ]);

    assert.equal(result.isolated, true);
  });

  it("ignores Report-Only policies", () => {
    const result = evaluateCrossOriginIsolation(
      headers(undefined, undefined, [
        [COOP_RO, "same-origin"],
        [COEP_RO, "require-corp"],
      ]),
    );

    assert.equal(result.isolated, false);
    assert.equal(result.coop.status, "missing");
    assert.equal(result.coep.status, "missing");
  });

  it("keeps enforcing the actual header when Report-Only is present", () => {
    const result = evaluateCrossOriginIsolation(
      headers("same-origin", "require-corp", [
        [COOP_RO, "unsafe-none"],
        [COEP_RO, "unsafe-none"],
      ]),
    );

    assert.equal(result.isolated, true);
  });

  it("rejects duplicated actual header lines even when values match", () => {
    const coopResult = evaluateCrossOriginIsolation(
      headers(["same-origin", "same-origin"], "require-corp"),
    );
    const coepResult = evaluateCrossOriginIsolation(
      headers("same-origin", ["require-corp", "require-corp"]),
    );

    assert.equal(coopResult.isolated, false);
    assert.equal(coopResult.coop.status, "duplicate");
    assert.equal(coepResult.isolated, false);
    assert.equal(coepResult.coep.status, "duplicate");
  });

  it("rejects case-variant duplicate field lines", () => {
    const result = evaluateCrossOriginIsolation([
      ["Cross-Origin-Opener-Policy", "same-origin"],
      ["cross-origin-opener-policy", "same-origin"],
      [COEP, "require-corp"],
    ]);

    assert.equal(result.isolated, false);
    assert.equal(result.coop.status, "duplicate");
  });

  it("rejects duplicate values collapsed into a comma-separated field", () => {
    const result = evaluateCrossOriginIsolation(
      headers("same-origin, same-origin", "require-corp"),
    );

    assert.equal(result.isolated, false);
    assert.equal(result.coop.status, "invalid");
  });

  it("rejects multiple tokens in one field", () => {
    const result = evaluateCrossOriginIsolation(
      headers("same-origin, unsafe-none", "require-corp"),
    );

    assert.equal(result.isolated, false);
    assert.equal(result.coop.status, "invalid");
  });

  it("rejects malformed COEP independently", () => {
    for (const value of [
      "require-corp, unsafe-none",
      '"require-corp"',
      "require-corp;report-to=123",
      "require-corp@evil",
    ]) {
      const result = evaluateCrossOriginIsolation(
        headers("same-origin", value),
      );
      assert.equal(result.isolated, false, value);
      assert.equal(result.coep.status, "invalid", value);
    }
  });

  it("rejects Structured Fields values of the wrong type", () => {
    for (const value of ['"same-origin"', "123", "?1", ":c2FtZS1vcmlnaW4=:"]) {
      const result = evaluateCrossOriginIsolation(
        headers(value, "require-corp"),
      );
      assert.equal(result.isolated, false, value);
      assert.equal(result.coop.status, "invalid", value);
    }
  });

  it("rejects malformed Structured Fields syntax", () => {
    const mutations = [
      "",
      "same-origin;",
      "same-origin;;",
      "same-origin;report-to=",
      'same-origin;report-to="unterminated',
      'same-origin;report-to="x"junk',
      "same-origin;Report-To=x",
      "same-origin;report-to=123",
      "same-origin;report-to",
      "same-origin /",
      "same-origin@evil",
      "same-origin\t",
      "same-origin\n",
      "same\u2011origin",
    ];

    for (const value of mutations) {
      const result = evaluateCrossOriginIsolation(
        headers(value, "require-corp"),
      );
      assert.equal(result.isolated, false, JSON.stringify(value));
      assert.equal(result.coop.status, "invalid", JSON.stringify(value));
    }
  });

  it("accepts valid Structured Fields parameters and whitespace", () => {
    const result = evaluateCrossOriginIsolation(
      headers(
        ' same-origin;report-to="default" ',
        ' require-corp;report-to="default";unknown=123 ',
      ),
    );

    assert.equal(result.isolated, true);
  });

  it("accepts valid parameter values containing commas", () => {
    const result = evaluateCrossOriginIsolation(
      headers('same-origin;report-to="a,b"', "require-corp"),
    );

    assert.equal(result.isolated, true);
  });

  it("rejects a header whose value has an invalid type", () => {
    const result = evaluateCrossOriginIsolation({
      [COOP]: 42 as unknown as string,
      [COEP]: "require-corp",
    });

    assert.equal(result.isolated, false);
    assert.equal(result.coop.status, "invalid");
  });

  it("supports plain objects with duplicated values in arrays", () => {
    const result = evaluateCrossOriginIsolation({
      [COOP]: ["same-origin", "same-origin"],
      [COEP]: "require-corp",
    });

    assert.equal(result.isolated, false);
    assert.equal(result.coop.status, "duplicate");
  });

  it("supports Headers instances while rejecting collapsed duplicates", () => {
    const valid = new Headers([
      [COOP, "same-origin"],
      [COEP, "require-corp"],
    ]);
    assert.equal(evaluateCrossOriginIsolation(valid).isolated, true);

    const duplicate = new Headers([
      [COOP, "same-origin"],
      [COOP, "same-origin"],
      [COEP, "require-corp"],
    ]);
    assert.equal(evaluateCrossOriginIsolation(duplicate).isolated, false);

    const spliced = new Headers();
    spliced.append(COOP, 'same-origin;report-to="a');
    spliced.append(COOP, 'b"');
    spliced.append(COEP, "require-corp");
    const splicedResult = evaluateCrossOriginIsolation(spliced);
    assert.equal(splicedResult.isolated, false);
    assert.equal(splicedResult.coop.status, "invalid");
  });

  it("reports specific failure reasons", () => {
    const result = evaluateCrossOriginIsolation(
      headers("same-origin-allow-popups", undefined),
    );

    assert.deepEqual(result.failures, [
      { header: COOP, reason: "ineffective" },
      { header: COEP, reason: "missing" },
    ]);
  });
});

describe("Structured Fields item parser", () => {
  it("parses a token with parameters", () => {
    const parsed = parseStructuredFieldItem(
      'same-origin;report-to="default";flag;count=10',
    );

    assert.ok(parsed);
    assert.deepEqual(parsed.item, { type: "token", value: "same-origin" });
    assert.deepEqual(parsed.parameters.get("report-to"), {
      type: "string",
      value: "default",
    });
    assert.deepEqual(parsed.parameters.get("flag"), {
      type: "boolean",
      value: true,
    });
    assert.deepEqual(parsed.parameters.get("count"), {
      type: "integer",
      value: 10,
    });
  });

  it("allows leading and trailing SP but no other field syntax", () => {
    assert.ok(parseStructuredFieldItem("  same-origin  "));
    assert.equal(parseStructuredFieldItem("same-origin,"), null);
    assert.equal(parseStructuredFieldItem("same-origin, require-corp"), null);
    assert.equal(parseStructuredFieldItem("same-origin\t"), null);
  });

  it("allows duplicate parameter keys and keeps the last value", () => {
    const parsed = parseStructuredFieldItem(
      'same-origin;report-to="first";report-to="last"',
    );

    assert.ok(parsed);
    assert.deepEqual(parsed.parameters.get("report-to"), {
      type: "string",
      value: "last",
    });
  });
});

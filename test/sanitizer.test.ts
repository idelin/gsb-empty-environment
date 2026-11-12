import { test } from "node:test";
import assert from "node:assert/strict";

import {
  annotate,
  arg,
  compare,
  constOperand,
  field,
  implement,
  isPoisoned,
  labelRef,
  lookupSummary,
  not,
  ref,
  resetRegistry,
  requireVerified,
  useSummary,
  verify,
} from "../src/index.ts";

function sanitizerContract() {
  return annotate("sanitizeUser", {
    trusted: { kind: "bool" },
    label: { kind: "label", labels: ["raw", "sanitized"] },
  })
    .pre()
    .returns(["sanitized"], [compare("eq", field("label"), constOperand("sanitized"))])
    .throws("InvariantError", [
      compare("eq", field("label"), constOperand("sanitized")),
    ])
    .build();
}

test("complete sanitizer verifies and yields a sealed summary", () => {
  resetRegistry();
  const contract = sanitizerContract();
  const implementation = implement("sanitizeUser", "entry")
    .node({ kind: "entry", id: "entry", next: "check" })
    .node({
      kind: "branch",
      id: "check",
      guard: compare("eq", field("trusted"), constOperand(true)),
      trueNext: "clean",
      falseNext: "fail",
    })
    .node({
      kind: "effects",
      id: "clean",
      effects: [labelRef(ref("label"), "sanitized")],
      next: "ok",
    })
    .node({ kind: "return", id: "ok", ref: ref("label") })
    .node({
      kind: "effects",
      id: "fail",
      effects: [labelRef(ref("label"), "sanitized")],
      next: "boom",
    })
    .node({ kind: "throw", id: "boom", tag: "InvariantError", handler: null })
    .build();

  const result = verify(contract, implementation);
  assert.equal(result.ok, true);
  if (result.ok) {
    const summary = useSummary(result);
    assert.equal(summary.id, "sanitizeUser");
    assert.equal(requireVerified(result), summary);
    assert.equal(lookupSummary("sanitizeUser")?.id, "sanitizeUser");
  }
});

test("raw early-return path fails and poisons the annotation", () => {
  resetRegistry();
  const contract = sanitizerContract();
  const implementation = implement("sanitizeUser", "entry")
    .node({ kind: "entry", id: "entry", next: "check" })
    .node({
      kind: "branch",
      id: "check",
      guard: compare("eq", field("trusted"), constOperand(true)),
      trueNext: "early",
      falseNext: "clean",
    })
    .node({ kind: "return", id: "early", ref: ref("label") })
    .node({
      kind: "effects",
      id: "clean",
      effects: [labelRef(ref("label"), "sanitized")],
      next: "ok",
    })
    .node({ kind: "return", id: "ok", ref: ref("label") })
    .build();

  const result = verify(contract, implementation);
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.counterexamples[0]?.reason, "return-label-not-allowed");
    assert.equal(isPoisoned("sanitizeUser"), true);
    assert.equal(lookupSummary("sanitizeUser"), null);
  }
});

test("exception exit with raw object state fails the post-condition", () => {
  resetRegistry();
  const contract = sanitizerContract();
  const implementation = implement("sanitizeUser", "entry")
    .node({ kind: "entry", id: "entry", next: "check" })
    .node({
      kind: "branch",
      id: "check",
      guard: compare("eq", field("trusted"), constOperand(true)),
      trueNext: "clean",
      falseNext: "boom",
    })
    .node({
      kind: "effects",
      id: "clean",
      effects: [labelRef(ref("label"), "sanitized")],
      next: "ok",
    })
    .node({ kind: "return", id: "ok", ref: ref("label") })
    .node({ kind: "throw", id: "boom", tag: "InvariantError", handler: null })
    .build();

  const result = verify(contract, implementation);
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.counterexamples[0]?.reason, "post-condition-failed");
    assert.equal(result.counterexamples[0]?.exitTag, "InvariantError");
    assert.equal(isPoisoned("sanitizeUser"), true);
  }
});

test("unsatisfiable preconditions cannot seal a vacuous summary", () => {
  resetRegistry();
  const contract = annotate("vacuous", {
    trusted: { kind: "bool" },
  })
    .pre(
      compare("eq", field("trusted"), constOperand(true)),
      not(compare("eq", field("trusted"), constOperand(true))),
    )
    .returns(["sanitized"])
    .build();
  const implementation = implement("vacuous", "entry")
    .node({ kind: "entry", id: "entry", next: "ok" })
    .node({ kind: "return", id: "ok", ref: ref("trusted") })
    .build();

  const result = verify(contract, implementation);
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.counterexamples[0]?.reason, "malformed-model");
    assert.equal(isPoisoned("vacuous"), true);
  }
});

test("dangling control-flow edge is rejected and poisons the id", () => {
  resetRegistry();
  const contract = sanitizerContract();
  const implementation = implement("sanitizeUser", "entry")
    .node({ kind: "entry", id: "entry", next: "missing" })
    .build();

  const result = verify(contract, implementation);
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.counterexamples[0]?.reason, "malformed-model");
    assert.equal(isPoisoned("sanitizeUser"), true);
    assert.equal(lookupSummary("sanitizeUser"), null);
  }
});

test("unhandled callee exception cannot rely on a failed annotation", () => {
  resetRegistry();
  const callerContract = annotate("caller", {
    label: { kind: "label", labels: ["raw", "sanitized"] },
  })
    .returns(["sanitized"])
    .build();
  const callerImpl = implement("caller", "entry")
    .node({ kind: "entry", id: "entry", next: "invoke" })
    .node({
      kind: "call",
      id: "invoke",
      callee: "sanitizeUser",
      args: [arg(0)],
      bindResult: ref("label"),
      next: "ok",
    })
    .node({ kind: "return", id: "ok", ref: ref("label") })
    .build();

  const result = verify(callerContract, callerImpl);
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.counterexamples[0]?.reason, "unverified-callee");
  }
});

test("verified callee summary drives both normal and exception call edges", () => {
  resetRegistry();
  const calleeContract = annotate("helper", {
    label: { kind: "label", labels: ["raw", "sanitized"] },
    mode: { kind: "bool" },
  })
    .returns(["sanitized"])
    .throws("Boom", [compare("eq", field("label"), constOperand("sanitized"))])
    .build();
  const calleeImpl = implement("helper", "entry")
    .node({ kind: "entry", id: "entry", next: "split" })
    .node({
      kind: "branch",
      id: "split",
      guard: compare("eq", field("mode"), constOperand(true)),
      trueNext: "clean",
      falseNext: "cleanThenThrow",
    })
    .node({
      kind: "effects",
      id: "clean",
      effects: [labelRef(ref("label"), "sanitized")],
      next: "ok",
    })
    .node({ kind: "return", id: "ok", ref: ref("label") })
    .node({
      kind: "effects",
      id: "cleanThenThrow",
      effects: [labelRef(ref("label"), "sanitized")],
      next: "boom",
    })
    .node({ kind: "throw", id: "boom", tag: "Boom", handler: null })
    .build();

  const calleeResult = verify(calleeContract, calleeImpl);
  assert.equal(calleeResult.ok, true);

  const callerContract = annotate("caller", {
    label: { kind: "label", labels: ["raw", "sanitized"] },
    mode: { kind: "bool" },
    lastError: { kind: "enum", values: ["none", "Boom"] },
  })
    .returns(["sanitized"])
    .throws("Boom", [compare("eq", field("lastError"), constOperand("Boom"))])
    .build();
  const callerImpl = implement("caller", "entry")
    .node({ kind: "entry", id: "entry", next: "invoke" })
    .node({
      kind: "call",
      id: "invoke",
      callee: "helper",
      args: [arg(0)],
      bindResult: ref("label"),
      bindObject: ref("lastError"),
      next: "ok",
      onThrow: "boom",
    })
    .node({ kind: "return", id: "ok", ref: ref("label") })
    .node({ kind: "throw", id: "boom", tag: "Boom", handler: null })
    .build();

  const result = verify(callerContract, callerImpl);
  assert.equal(result.ok, true);
  if (result.ok) {
    assert.equal(useSummary(result).id, "caller");
  }
});

import { test } from "node:test";
import assert from "node:assert/strict";
import { Op, assemble, decode } from "../src/isa.ts";
import { verify, replayPath, formatErrors, type VerifyError } from "../src/verifier.ts";

function errorsOf(code: Uint8Array): VerifyError[] {
  const r = verify(code);
  assert.equal(r.ok, false, "expected verification to fail");
  return (r as { ok: false; errors: VerifyError[] }).errors;
}

test("accepts straight-line program ending in RET with height 1", () => {
  const code = assemble([["push", 1], ["push", 2], ["add"], ["ret"]]);
  const r = verify(code);
  assert.equal(r.ok, true);
  if (r.ok) {
    assert.deepEqual([...r.heights.entries()], [[0, 0], [2, 1], [4, 2], [5, 1]]);
  }
});

test("accepts balanced branch diamond", () => {
  const code = assemble([
    ["push", 10],
    ["jz", "else"],
    ["push", 1],
    ["jmp", "end"],
    ["label", "else"],
    ["push", 2],
    ["label", "end"],
    ["ret"],
  ]);
  assert.equal(verify(code).ok, true);
});

test("accepts constant-height infinite loop", () => {
  const code = assemble([["label", "l"], ["jmp", "l"]]);
  assert.equal(verify(code).ok, true);
});

test("rejects stack underflow with path evidence", () => {
  const code = assemble([["push", 1], ["pop"], ["pop"]]);
  const errs = errorsOf(code);
  assert.equal(errs.length, 1);
  const e = errs[0];
  assert.equal(e.kind, "underflow");
  if (e.kind === "underflow") {
    assert.equal(e.offset, 3);
    assert.deepEqual(e.path, [0, 2, 3]);
    const steps = replayPath(code, e.path);
    assert.equal(steps[steps.length - 1].underflow, true);
  }
});

test("rejects RET with stack height != 1", () => {
  const code = assemble([["push", 1], ["push", 2], ["ret"]]);
  const errs = errorsOf(code);
  assert.equal(errs[0].kind, "ret-height");
  const e = errs[0];
  if (e.kind === "ret-height") assert.equal(e.height, 2);
});

test("rejects RET on empty stack", () => {
  const errs = errorsOf(assemble([["ret"]]));
  assert.equal(errs[0].kind, "ret-height");
});

test("rejects height conflict in a growing loop, with two witness paths", () => {
  const code = assemble([["label", "l"], ["push", 1], ["jmp", "l"]]);
  const errs = errorsOf(code);
  assert.equal(errs[0].kind, "height-conflict");
  const e = errs[0];
  if (e.kind === "height-conflict") {
    assert.equal(e.offset, 0);
    assert.equal(e.heightA, 0);
    assert.equal(e.heightB, 1);
    const a = replayPath(code, e.pathA);
    const b = replayPath(code, e.pathB);
    assert.equal(a[a.length - 1].heightBefore, 0);
    assert.equal(b[b.length - 1].heightBefore, 1);
  }
});

test("rejects height conflict at a branch merge", () => {
  const code = assemble([
    ["push", 0],
    ["jz", "join"],
    ["push", 1],
    ["label", "join"],
    ["push", 2],
    ["ret"],
  ]);
  const errs = errorsOf(code);
  assert.deepEqual(errs.map((e) => e.kind), ["height-conflict"]);
  const e = errs[0];
  if (e.kind === "height-conflict") {
    assert.equal(e.heightA === 0 || e.heightB === 0, true);
    assert.notEqual(e.heightA, e.heightB);
  }
});

test("rejects jump to non-boundary and out-of-range targets", () => {
  // PUSH occupies bytes 0..1, so target 1 is mid-instruction.
  const mid = Uint8Array.from([Op.PUSH, 0, Op.JMP, 1]);
  const errs1 = errorsOf(mid);
  assert.equal(errs1[0].kind, "bad-target");
  if (errs1[0].kind === "bad-target") assert.equal(errs1[0].target, 1);

  const out = Uint8Array.from([Op.JMP, 99]);
  assert.equal(errorsOf(out)[0].kind, "bad-target");
});

test("rejects undecodable bytes", () => {
  assert.equal(errorsOf(Uint8Array.from([0xff]))[0].kind, "decode");
  assert.equal(errorsOf(Uint8Array.from([Op.PUSH]))[0].kind, "decode"); // truncated
});

test("unreachable bad code is NOT reported", () => {
  const cases: Array<[string, Uint8Array]> = [
    ["underflow after RET", assemble([["push", 1], ["ret"], ["pop"]])],
    ["underflow after JMP", assemble([["jmp", "ok"], ["pop"], ["label", "ok"], ["push", 1], ["ret"]])],
    ["bad RET height after RET", assemble([["push", 1], ["ret"], ["push", 1], ["push", 2], ["ret"]])],
    ["growing loop after RET", assemble([["push", 1], ["ret"], ["label", "l"], ["push", 1], ["jmp", "l"]])],
    [
      "conflict diamond after RET",
      assemble([
        ["push", 1],
        ["ret"],
        ["push", 0],
        ["jz", "join"],
        ["push", 9],
        ["label", "join"],
        ["pop"],
      ]),
    ],
  ];
  for (const [name, code] of cases) {
    const r = verify(code);
    assert.equal(r.ok, true, `false positive on unreachable bad code: ${name}\n${r.ok ? "" : formatErrors(code, r.errors)}`);
  }
});

test("bad code becomes an error once made reachable", () => {
  // Same dead POP as before, but now reached by a fallthrough.
  const code = assemble([["push", 1], ["pop"], ["pop"], ["ret"]]);
  assert.equal(verify(code).ok, false);
});

test("evidence formatting renders both conflict paths", () => {
  const code = assemble([["label", "l"], ["push", 1], ["jmp", "l"]]);
  const r = verify(code);
  assert.equal(r.ok, false);
  if (!r.ok) {
    const text = formatErrors(code, r.errors);
    assert.match(text, /conflicting entry stack heights at 0: 0 vs 1/);
    assert.match(text, /path A/);
    assert.match(text, /path B/);
    assert.match(text, /0: PUSH 1/);
  }
});

test("decode records instruction boundaries", () => {
  const code = assemble([["push", 1], ["pop"], ["ret"]]);
  const d = decode(code);
  assert.equal(d.ok, true);
  if (d.ok) assert.deepEqual([...d.byOffset.keys()], [0, 2, 3]);
});

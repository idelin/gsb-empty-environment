import assert from "node:assert/strict";
import test from "node:test";

import { bruteForceRepair } from "../src/bruteforce.ts";
import { parses } from "../src/grammar.ts";
import {
  applyEdits,
  compareEditScripts,
  repair,
  repairTokens,
  tokensToSource,
  type Edit,
  type RepairOk,
} from "../src/repair.ts";
import { tokenize, type Token, type TokenType } from "../src/tokens.ts";

const ok = (source: string, budget?: number): RepairOk => {
  const result = repair(source, budget === undefined ? {} : { budget });
  assert.equal(result.status, "ok", `expected ok for ${JSON.stringify(source)}`);
  return result;
};

const ins = (gap: number, token: TokenType): Edit => ({ kind: "insert", gap, token });
const del = (index: number): Edit => ({ kind: "delete", index });

test("repair result includes the parse tree", () => {
  const result = ok("1+2*3");
  assert.deepEqual(result.ast, {
    kind: "bin",
    op: "+",
    left: { kind: "int", value: "1" },
    right: {
      kind: "bin",
      op: "*",
      left: { kind: "int", value: "2" },
      right: { kind: "int", value: "3" },
    },
  });
  // "f(1,]" -> "f(1,[])": two insertions, still a call.
  const repaired = ok("f(1,]");
  assert.equal(repaired.cost, 2);
  assert.equal(repaired.ast.kind, "call");
});

test("valid inputs need no edits", () => {
  for (const src of ["1", "x", "1+2*3", "f(1,[2,3])", "[]", "f()", "(1)", "a-b/c+d", "[1,[2],f(3)]"]) {
    const result = ok(src);
    assert.equal(result.cost, 0, src);
    assert.deepEqual(result.edits, [], src);
  }
});

test("known optimal repairs", () => {
  // "1 + * 2": deleting '+' (index 1) beats deleting '*' (index 2) and
  // beats inserting an operand at gap 2, because position 1 < 2.
  let result = ok("1 + * 2");
  assert.equal(result.cost, 1);
  assert.deepEqual(result.edits, [del(1)]);
  assert.equal(tokensToSource(result.repairedTokens), "1 * 2");

  // "(1": deleting '(' at position 0 beats inserting ')' at the end.
  result = ok("(1");
  assert.equal(result.cost, 1);
  assert.deepEqual(result.edits, [del(0)]);

  // "f(": deleting '(' wins over inserting ')' (position 1 < 2).
  result = ok("f(");
  assert.equal(result.cost, 1);
  assert.deepEqual(result.edits, [del(1)]);

  // Empty input: a single inserted INT is the cheapest program.
  result = ok("");
  assert.equal(result.cost, 1);
  assert.deepEqual(result.edits, [ins(0, "INT")]);

  // Lexical errors are deletable INVALID tokens; "1 @ 2" needs two edits.
  // Deleting '1' and '@' (positions 0 and 1) beats any script whose first
  // edit is at position >= 1, e.g. inserting '*' before '@' and deleting it.
  result = ok("1 @ 2");
  assert.equal(result.cost, 2);
  assert.deepEqual(result.edits, [del(0), del(1)]);
  assert.equal(tokensToSource(result.repairedTokens), "2");
});

test("repaired output always parses and preserves positions", () => {
  const samples = ["1 + * 2", "f(1,,2)", "[[[", "1 2 3", "a(", "(1]", "f(]", "1+*", ",1", "1,", "[1,]"];
  for (const src of samples) {
    const tokens = tokenize(src);
    const result = ok(src);
    const repaired = applyEdits(tokens, result.edits);
    assert.ok(parses(repaired.map((t) => t.type)), `${src} -> ${tokensToSource(repaired)}`);
    // Kept tokens keep their original positions, in order.
    const kept = repaired.filter((t) => tokens.some((o) => o.pos === t.pos && o.text === t.text));
    for (let i = 1; i < kept.length; i++) assert.ok(kept[i].pos > kept[i - 1].pos);
  }
});

test("position preservation is exact", () => {
  // "+ 10": at position 0 an insert sorts before a delete, so the optimal
  // script inserts an operand instead of deleting the operator.
  const result = ok("+ 10");
  assert.equal(result.cost, 1);
  assert.deepEqual(result.edits, [ins(0, "INT")]);
  // Kept tokens retain original positions; the inserted token is anchored
  // at the insertion point (position of the following original token).
  assert.deepEqual(
    result.repairedTokens.map((t) => [t.text, t.pos]),
    [["0", 0], ["+", 0], ["10", 2]],
  );
});

test("budget exhaustion reports only bounds", () => {
  const hard = "((((((((((1";
  const full = ok(hard);
  const limited = repair(hard, { budget: 5 });
  assert.equal(limited.status, "budget-exceeded");
  if (limited.status !== "budget-exceeded") return;
  assert.ok(limited.lowerBound <= full.cost, `lower ${limited.lowerBound} <= ${full.cost}`);
  assert.ok(limited.upperBound >= full.cost, `upper ${limited.upperBound} >= ${full.cost}`);
  assert.ok(limited.upperBound <= hard.length + 1);
  assert.ok(!("edits" in limited), "no repair is reported when over budget");
});

test("zero-length and single-token edge cases match brute force", () => {
  for (const src of ["", "(", ")", "+", "1", "f", ",", "[", "]"]) {
    const tokens = tokenize(src);
    const expected = bruteForceRepair(tokens);
    assert.ok(expected !== null);
    const result = repairTokens(tokens);
    assert.equal(result.status, "ok", src);
    if (result.status !== "ok") return;
    assert.equal(result.cost, expected.cost, src);
    assert.deepEqual(result.edits, expected.edits, src);
  }
});

// ---------------------------------------------------------------------------
// Exhaustive cross-check: every token string up to length 3 over a 5-symbol
// alphabet (156 inputs), plus random longer inputs. The brute-force
// enumerator materializes outputs and checks grammaticality only at the end,
// so it independently validates both the cost and the exact edit script.
// ---------------------------------------------------------------------------

const ALPHABET: Token[] = [
  { type: "INT", text: "1", pos: 0 },
  { type: "IDENT", text: "f", pos: 0 },
  { type: "LPAREN", text: "(", pos: 0 },
  { type: "RPAREN", text: ")", pos: 0 },
  { type: "PLUS", text: "+", pos: 0 },
];

function* enumerateInputs(maxLen: number): Generator<Token[]> {
  const total = ALPHABET.length;
  for (let len = 0; len <= maxLen; len++) {
    const count = total ** len;
    for (let code = 0; code < count; code++) {
      const tokens: Token[] = [];
      let rest = code;
      for (let i = 0; i < len; i++) {
        tokens.push({ ...ALPHABET[rest % total], pos: i });
        rest = Math.floor(rest / total);
      }
      yield tokens;
    }
  }
}

test("exhaustive short strings match brute force", () => {
  let checked = 0;
  for (const tokens of enumerateInputs(3)) {
    const result = repairTokens(tokens);
    assert.equal(result.status, "ok");
    if (result.status !== "ok") return;
    const input = tokens.map((t) => t.text).join(" ");
    // The main result must be a genuine repair.
    assert.ok(
      parses(applyEdits(tokens, result.edits).map((t) => t.type)),
      `invalid repair on ${JSON.stringify(input)}`,
    );
    // Independent optimality check: enumerate every script costing no more
    // than the reported cost and confirm none is cheaper or smaller.
    const expected = bruteForceRepair(tokens, { costCap: result.cost });
    assert.ok(expected !== null, "brute force must finish for short inputs");
    assert.equal(result.cost, expected.cost, `cost mismatch on ${JSON.stringify(input)}`);
    assert.equal(
      compareEditScripts(result.edits, expected.edits),
      0,
      `script mismatch on ${JSON.stringify(input)}: ` +
        `got ${JSON.stringify(result.edits)}, want ${JSON.stringify(expected.edits)}`,
    );
    checked++;
  }
  assert.equal(checked, 1 + 5 + 25 + 125);
});

test("random longer inputs match brute force", () => {
  let seed = 0x2f6e2b1;
  const rand = (): number => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed;
  };
  let checked = 0;
  let skipped = 0;
  for (let trial = 0; trial < 40; trial++) {
    const len = 4 + (rand() % 2); // 4..5 tokens (brute force packs <= 11)
    const tokens: Token[] = [];
    for (let i = 0; i < len; i++) tokens.push({ ...ALPHABET[rand() % ALPHABET.length], pos: i });
    const result = repairTokens(tokens);
    assert.equal(result.status, "ok");
    if (result.status !== "ok") return;
    const input = tokens.map((t) => t.text).join(" ");
    assert.ok(
      parses(applyEdits(tokens, result.edits).map((t) => t.type)),
      `invalid repair on ${JSON.stringify(input)}`,
    );
    const expected = bruteForceRepair(tokens, { costCap: result.cost, expansionCap: 300_000 });
    if (expected === null) {
      skipped++;
      continue;
    }
    assert.equal(result.cost, expected.cost, `cost mismatch on ${JSON.stringify(input)}`);
    assert.equal(
      compareEditScripts(result.edits, expected.edits),
      0,
      `script mismatch on ${JSON.stringify(input)}`,
    );
    checked++;
  }
  assert.ok(checked >= 25, `too many skipped: ${skipped}`);
});

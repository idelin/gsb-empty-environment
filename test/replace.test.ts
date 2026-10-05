import { test } from "node:test";
import assert from "node:assert/strict";
import { Rope } from "../src/rope.ts";
import { replaceBatch, applyEdits, isRuleActive } from "../src/engine.ts";
import type { Rule } from "../src/engine.ts";

const run = (text: string, rules: Rule[]) => replaceBatch(Rope.of(text), rules);

test("matches only on word boundaries", () => {
  const result = run("concatenate cat scat cat.", [
    { id: "r1", pattern: "cat", replacement: "dog" },
  ]);
  assert.equal(result.text, "concatenate dog scat dog.");
  assert.equal(result.adopted.length, 2);
  assert.deepEqual(
    result.adopted.map((c) => [c.start, c.end]),
    [
      [12, 15],
      [21, 24],
    ],
  );
});

test("maps replacement case through the finite case table", () => {
  const result = run("cat Cat CAT cAt", [
    { id: "r1", pattern: "cat", replacement: "dog" },
  ]);
  // lower -> lower, capitalized -> capitalized, upper -> upper, mixed -> lower fallback
  assert.equal(result.text, "dog Dog DOG dog");
});

test("lengthening replacement keeps ranges in original coordinates", () => {
  const original = "I write js and js daily.";
  const result = run(original, [{ id: "r1", pattern: "js", replacement: "javascript" }]);
  assert.equal(result.text, "I write javascript and javascript daily.");
  assert.equal(result.adopted.length, 2);
  for (const c of result.adopted) {
    assert.equal(original.slice(c.start, c.end), "js");
    assert.equal(c.replacement, "javascript");
  }
});

test("shortening replacement", () => {
  const result = run("JavaScript rules, javascript rules.", [
    { id: "r1", pattern: "javascript", replacement: "js" },
  ]);
  // "JavaScript" is mixed case, which the finite table falls back to lower.
  assert.equal(result.text, "js rules, js rules.");
  assert.deepEqual(
    result.adopted.map((c) => [c.start, c.end]),
    [
      [0, 10],
      [18, 28],
    ],
  );
});

test("zero-length patterns and disabled rules never match", () => {
  assert.equal(isRuleActive({ id: "z", pattern: "", replacement: "x" }), false);
  assert.equal(isRuleActive({ id: "d", pattern: "cat", replacement: "x", enabled: false }), false);
  const result = run("cat cat", [
    { id: "z", pattern: "", replacement: "x" },
    { id: "d", pattern: "cat", replacement: "dog", enabled: false },
  ]);
  assert.equal(result.text, "cat cat");
  assert.equal(result.adopted.length, 0);
  assert.equal(result.shadowed.length, 0);
  assert.equal(result.edits.length, 0);
});

test("matches spanning rope chunk boundaries", () => {
  const rope = Rope.from(["I love jav", "as", "cript a lot"]);
  const result = replaceBatch(rope, [
    { id: "r1", pattern: "javascript", replacement: "js" },
  ]);
  assert.equal(result.text, "I love js a lot");
  assert.equal(result.adopted.length, 1);
  assert.deepEqual([result.adopted[0].start, result.adopted[0].end], [7, 17]);
  assert.equal(result.adopted[0].matched, "javascript");
});

test("conflicts resolve by priority, then rule id", () => {
  const byPriority = run("cat", [
    { id: "low", pattern: "cat", replacement: "dog", priority: 1 },
    { id: "high", pattern: "cat", replacement: "fox", priority: 9 },
  ]);
  assert.equal(byPriority.text, "fox");
  assert.equal(byPriority.adopted[0].ruleId, "high");
  assert.equal(byPriority.shadowed.length, 1);
  assert.equal(byPriority.shadowed[0].ruleId, "low");
  assert.equal(byPriority.shadowed[0].shadowedBy, "high");
  assert.deepEqual([byPriority.shadowed[0].start, byPriority.shadowed[0].end], [0, 3]);

  const byId = run("cat", [
    { id: "b", pattern: "cat", replacement: "dog" },
    { id: "a", pattern: "cat", replacement: "fox" },
  ]);
  assert.equal(byId.text, "fox");
  assert.equal(byId.adopted[0].ruleId, "a");
  assert.equal(byId.shadowed[0].ruleId, "b");
});

test("higher priority wins even when it starts later", () => {
  const result = run("foo bar baz", [
    { id: "left", pattern: "foo bar", replacement: "x", priority: 1 },
    { id: "right", pattern: "bar baz", replacement: "y", priority: 5 },
  ]);
  assert.equal(result.text, "foo y");
  assert.equal(result.adopted[0].ruleId, "right");
  assert.equal(result.shadowed[0].ruleId, "left");
  assert.equal(result.shadowed[0].shadowedBy, "right");
});

test("replacements never trigger other rules", () => {
  const rules: Rule[] = [
    { id: "r1", pattern: "foo", replacement: "bar", priority: 10 },
    { id: "r2", pattern: "bar", replacement: "baz", priority: 5 },
  ];
  // r1's output "bar" must not be re-matched by r2.
  assert.equal(run("foo", rules).text, "bar");
  // r2 still applies to "bar" present in the original text.
  assert.equal(run("foo bar", rules).text, "bar baz");
});

test("reverse patch restores the original text", () => {
  const original = "JS and JavaScript, CAT and cat. foo!";
  const rules: Rule[] = [
    { id: "grow", pattern: "js", replacement: "javascript", priority: 2 },
    { id: "shrink", pattern: "javascript", replacement: "ts", priority: 1 },
    { id: "swap", pattern: "cat", replacement: "dog" },
    { id: "drop", pattern: "foo", replacement: "bar", enabled: true },
  ];
  const rope = Rope.from(["JS and Java", "Script, CAT", " and cat. foo!"]);
  const result = replaceBatch(rope, rules);
  // "JS" is upper -> "JAVASCRIPT"; "JavaScript" is mixed -> lower fallback "ts".
  assert.equal(result.text, "JAVASCRIPT and ts, DOG and dog. bar!");
  assert.ok(result.edits.length > 0);
  const restored = applyEdits(result.text, result.reverseEdits);
  assert.equal(restored, original);
});

test("reverse patch round-trips with empty result", () => {
  const result = run("cat", [{ id: "r1", pattern: "cat", replacement: "" }]);
  assert.equal(result.text, "");
  assert.equal(applyEdits(result.text, result.reverseEdits), "cat");
});

test("applyEdits rejects overlapping edits", () => {
  assert.throws(
    () => applyEdits("abcdef", [{ start: 0, end: 3, text: "x" }, { start: 2, end: 4, text: "y" }]),
    RangeError,
  );
});

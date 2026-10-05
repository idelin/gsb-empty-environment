import { describe, expect, test } from "bun:test";
import {
  applyEdits,
  detectCaseStyle,
  editsFromCandidates,
  findCandidates,
  invertEdits,
  replace,
  Rope,
  type Rule,
} from "../src";

describe("word boundaries", () => {
  test("matches whole words only", () => {
    const r = replace("concatenate cat scatter", [
      { id: 1, pattern: "cat", replacement: "dog" },
    ]);
    expect(r.text).toBe("concatenate dog scatter");
    expect(r.adopted).toHaveLength(1);
    expect(r.adopted[0].start).toBe(12);
    expect(r.adopted[0].end).toBe(15);
  });

  test("underscore and digits count as word chars", () => {
    const r = replace("my_cat cat2 cat", [{ id: 1, pattern: "cat", replacement: "dog" }]);
    expect(r.text).toBe("my_cat cat2 dog");
  });
});

describe("finite case table", () => {
  test("detects styles", () => {
    expect(detectCaseStyle("cat")).toBe("lower");
    expect(detectCaseStyle("CAT")).toBe("upper");
    expect(detectCaseStyle("Cat")).toBe("capitalized");
    expect(detectCaseStyle("cAt")).toBe("other");
  });

  test("recases replacement through the default table", () => {
    const r = replace("cat CAT Cat", [{ id: 1, pattern: "cat", replacement: "dog" }]);
    expect(r.text).toBe("dog DOG Dog");
  });

  test("missing table entry keeps replacement as written", () => {
    const r = replace(
      "cat CAT",
      [{ id: 1, pattern: "cat", replacement: "Dog" }],
      { caseTable: { lower: (s) => s.toLowerCase() } },
    );
    expect(r.text).toBe("dog Dog");
  });

  test("case-sensitive rules skip the table", () => {
    const r = replace("cat CAT", [
      { id: 1, pattern: "cat", replacement: "Dog", caseSensitive: true },
    ]);
    expect(r.text).toBe("Dog CAT");
  });
});

describe("length changes", () => {
  test("lengthening replacement", () => {
    const r = replace("a cat sat", [{ id: 1, pattern: "cat", replacement: "caterpillar" }]);
    expect(r.text).toBe("a caterpillar sat");
    expect(r.adopted[0]).toMatchObject({ start: 2, end: 5, matched: "cat" });
  });

  test("shortening replacement", () => {
    const r = replace("a caterpillar sat", [
      { id: 1, pattern: "caterpillar", replacement: "cat" },
    ]);
    expect(r.text).toBe("a cat sat");
    expect(r.adopted[0]).toMatchObject({ start: 2, end: 13 });
  });

  test("deletion via empty replacement", () => {
    const r = replace("a very big cat", [{ id: 1, pattern: "very", replacement: "" }]);
    expect(r.text).toBe("a  big cat");
    expect(r.adopted[0]).toMatchObject({ start: 2, end: 6, replacement: "" });
  });
});

describe("zero-length and disabled rules", () => {
  test("empty pattern disables the rule", () => {
    const r = replace("cat", [
      { id: 1, pattern: "", replacement: "x" },
      { id: 2, pattern: "cat", replacement: "dog" },
    ]);
    expect(r.text).toBe("dog");
    expect(r.disabledRules).toEqual([1]);
  });

  test("enabled: false disables the rule", () => {
    const r = replace("cat", [
      { id: 7, pattern: "cat", replacement: "x", enabled: false },
    ]);
    expect(r.text).toBe("cat");
    expect(r.disabledRules).toEqual([7]);
    expect(r.adopted).toHaveLength(0);
  });
});

describe("rope chunks", () => {
  test("matches across chunk boundaries", () => {
    const rope = Rope.of("the qu", "ick br", "own fox");
    const r = replace(rope, [
      { id: 1, pattern: "quick", replacement: "swift" },
      { id: 2, pattern: "brown", replacement: "red" },
    ]);
    expect(r.text).toBe("the swift red fox");
    expect(r.adopted.map((c) => [c.start, c.end])).toEqual([
      [4, 9],
      [10, 15],
    ]);
  });

  test("word boundary is not inferred from chunk boundary", () => {
    const rope = Rope.of("scat", "ter");
    const r = replace(rope, [{ id: 1, pattern: "cat", replacement: "dog" }]);
    expect(r.text).toBe("scatter");
    expect(r.adopted).toHaveLength(0);
  });

  test("findCandidates works on ropes", () => {
    const rope = new Rope(["ab", "cd", "ef"]);
    const { candidates } = findCandidates(rope, [
      { id: 1, pattern: "abcdef", replacement: "x" },
    ]);
    expect(candidates).toHaveLength(1);
    expect(candidates[0].matched).toBe("abcdef");
  });
});

describe("conflict resolution", () => {
  test("priority wins, then rule id", () => {
    const rules: Rule[] = [
      { id: 1, pattern: "abc def", replacement: "x", priority: 1 },
      { id: 2, pattern: "def", replacement: "y", priority: 2 },
    ];
    const r = replace("abc def", rules);
    expect(r.text).toBe("abc y");
    expect(r.adopted.map((c) => c.ruleId)).toEqual([2]);
    expect(r.shadowed.map((c) => c.ruleId)).toEqual([1]);
    // shadowed candidate keeps its original span
    expect(r.shadowed[0]).toMatchObject({ start: 0, end: 7, matched: "abc def" });
  });

  test("equal priority falls back to rule id", () => {
    const rules: Rule[] = [
      { id: 5, pattern: "abc def", replacement: "x" },
      { id: 3, pattern: "def", replacement: "y" },
    ];
    const r = replace("abc def", rules);
    expect(r.text).toBe("abc y");
    expect(r.adopted.map((c) => c.ruleId)).toEqual([3]);
    expect(r.shadowed.map((c) => c.ruleId)).toEqual([5]);
  });

  test("non-overlapping same-rule matches are all adopted", () => {
    const r = replace("cat cat", [{ id: 1, pattern: "cat", replacement: "dog" }]);
    expect(r.text).toBe("dog dog");
    expect(r.adopted).toHaveLength(2);
  });
});

describe("no cascading", () => {
  test("a replacement never triggers another rule", () => {
    const r = replace("foo", [
      { id: 1, pattern: "foo", replacement: "bar" },
      { id: 2, pattern: "bar", replacement: "baz", priority: 10 },
    ]);
    expect(r.text).toBe("bar");
  });

  test("rule 2 still applies to original occurrences", () => {
    const r = replace("foo bar", [
      { id: 1, pattern: "foo", replacement: "bar" },
      { id: 2, pattern: "bar", replacement: "baz" },
    ]);
    expect(r.text).toBe("bar baz");
  });
});

describe("reverse patch", () => {
  test("inverse edits restore the original (mixed lengthen/shorten/delete)", () => {
    const original = "The quick brown fox jumps over the lazy dog";
    const rules: Rule[] = [
      { id: 1, pattern: "quick", replacement: "extraordinarily quick" },
      { id: 2, pattern: "brown", replacement: "red" },
      { id: 3, pattern: "lazy", replacement: "" },
      { id: 4, pattern: "fox", replacement: "Fox" },
    ];
    const r = replace(original, rules);
    const edits = editsFromCandidates(r.adopted);
    const patched = applyEdits(original, edits);
    expect(patched).toBe(r.text);
    const inverse = invertEdits(original, edits);
    expect(applyEdits(patched, inverse)).toBe(original);
  });

  test("roundtrip on rope-sourced text with adjacent edits", () => {
    const original = "cat dog";
    const rope = Rope.of("ca", "t d", "og");
    const r = replace(rope, [
      { id: 1, pattern: "cat", replacement: "kitten" },
      { id: 2, pattern: "dog", replacement: "puppy" },
    ]);
    expect(r.text).toBe("kitten puppy");
    const edits = editsFromCandidates(r.adopted);
    const inverse = invertEdits(original, edits);
    expect(applyEdits(r.text, inverse)).toBe(original);
  });
});

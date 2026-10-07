import { describe, expect, test } from "bun:test";
import {
  compareByteOrder,
  findOccurrences,
  longestNonOverlappingRepeat,
  selectNonOverlapping,
} from "../src/index";
import {
  bestSelectionExhaustive,
  bruteForceLongestRepeat,
} from "./brute";

describe("directed cases", () => {
  test("empty input returns empty", () => {
    expect(longestNonOverlappingRepeat("", 1)).toBeNull();
    expect(longestNonOverlappingRepeat("", 5)).toBeNull();
  });

  test("rejects non-positive or non-integer k", () => {
    expect(() => longestNonOverlappingRepeat("abc", 0)).toThrow(RangeError);
    expect(() => longestNonOverlappingRepeat("abc", -1)).toThrow(RangeError);
    expect(() => longestNonOverlappingRepeat("abc", 1.5)).toThrow(RangeError);
  });

  test("k = 1 yields the whole text", () => {
    const result = longestNonOverlappingRepeat("abc", 1);
    expect(result).toEqual({
      substring: "abc",
      starts: [0],
      occurrenceCount: 1,
      maxNonOverlapping: 1,
    });
  });

  test("distinguishes occurrence count from selectable non-overlapping count", () => {
    // "aa" occurs 3 times in "aaaa" but only 2 fit without overlapping,
    // so for k = 3 the answer falls back to the single character "a".
    const result = longestNonOverlappingRepeat("aaaa", 3);
    expect(result).toEqual({
      substring: "a",
      starts: [0, 1, 2, 3],
      occurrenceCount: 4,
      maxNonOverlapping: 4,
    });
    expect(findOccurrences("aaaa", "aa")).toEqual([0, 1, 2]);
    expect(selectNonOverlapping([0, 1, 2], 2)).toEqual([0, 2]);
  });

  test("overlapping occurrences still count once selectable", () => {
    const result = longestNonOverlappingRepeat("abab", 2);
    expect(result).toEqual({
      substring: "ab",
      starts: [0, 2],
      occurrenceCount: 2,
      maxNonOverlapping: 2,
    });
  });

  test("no qualifying substring returns null", () => {
    expect(longestNonOverlappingRepeat("ab", 2)).toBeNull();
    expect(longestNonOverlappingRepeat("abc", 4)).toBeNull();
  });

  test("ties on length break by byte order", () => {
    // Both "cat" and "dog" occur twice non-overlapping and no length-4
    // substring repeats; "cat" sorts first in byte order.
    const result = longestNonOverlappingRepeat("catXdogYcatZdog", 2);
    expect(result?.substring).toBe("cat");
    expect(result?.starts).toEqual([0, 8]);
  });

  test("adjacent repeats combine into a longer answer", () => {
    // "catdog" itself occurs twice back to back, outranking "cat"/"dog".
    const result = longestNonOverlappingRepeat("catdogcatdog", 2);
    expect(result?.substring).toBe("catdog");
    expect(result?.starts).toEqual([0, 6]);
  });

  test("byte order follows UTF-16 code units", () => {
    expect(compareByteOrder("apple", "apply")).toBeLessThan(0);
    expect(compareByteOrder("ab", "abc")).toBeLessThan(0);
    expect(compareByteOrder("abc", "abc")).toBe(0);
    // Astral characters sort by surrogate code units, before U+FFFF.
    expect(compareByteOrder("😀", "￿")).toBeLessThan(0);
  });

  test("greedy selection matches exhaustive selection on occurrence patterns", () => {
    // Every binary text up to length 9, every substring: greedy interval
    // scheduling must equal brute-force subset enumeration.
    const alphabet = ["a", "b"];
    const texts: string[] = [""];
    for (let length = 1; length <= 9; length++) {
      for (let mask = 0; mask < 1 << length; mask++) {
        let text = "";
        for (let bit = 0; bit < length; bit++) {
          text += alphabet[(mask >> bit) & 1];
        }
        texts.push(text);
      }
    }
    for (const text of texts) {
      for (let length = 1; length <= text.length; length++) {
        for (let i = 0; i + length <= text.length; i++) {
          const starts = findOccurrences(text, text.slice(i, i + length));
          expect(selectNonOverlapping(starts, length)).toEqual(
            bestSelectionExhaustive(starts, length),
          );
        }
      }
    }
  });
});

describe("cross-check against brute force", () => {
  const check = (text: string, k: number) => {
    const expected = bruteForceLongestRepeat(text, k);
    const actual = longestNonOverlappingRepeat(text, k);
    expect(actual).toEqual(expected);
  };

  test("every binary text up to length 8, k in 1..4", () => {
    const alphabet = ["a", "b"];
    for (let length = 0; length <= 8; length++) {
      for (let mask = 0; mask < 1 << length; mask++) {
        let text = "";
        for (let bit = 0; bit < length; bit++) {
          text += alphabet[(mask >> bit) & 1];
        }
        for (let k = 1; k <= 4; k++) check(text, k);
      }
    }
  });

  test("every ternary text up to length 6, k in 1..3", () => {
    const alphabet = ["a", "b", "c"];
    for (let length = 0; length <= 6; length++) {
      const total = 3 ** length;
      for (let code = 0; code < total; code++) {
        let text = "";
        for (let pos = 0, rest = code; pos < length; pos++, rest = (rest / 3) | 0) {
          text += alphabet[rest % 3];
        }
        for (let k = 1; k <= 3; k++) check(text, k);
      }
    }
  });

  test("seeded random texts, varied alphabets including astral characters", () => {
    let state = 0x9e3779b9;
    const random = () => {
      state |= 0;
      state = (state + 0x6d2b79f5) | 0;
      let t = Math.imul(state ^ (state >>> 15), 1 | state);
      t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
    const alphabets = [
      ["a", "b"],
      ["a", "b", "c"],
      ["é", "b", "😀"],
      ["x", "y", "z", "w"],
    ];
    for (let iteration = 0; iteration < 400; iteration++) {
      const alphabet = alphabets[iteration % alphabets.length];
      const length = Math.floor(random() * 12);
      let text = "";
      for (let i = 0; i < length; i++) {
        text += alphabet[Math.floor(random() * alphabet.length)];
      }
      const k = 1 + Math.floor(random() * 5);
      check(text, k);
    }
  });
});

import test from "node:test";
import assert from "node:assert/strict";
import {
  buildDictionary,
  DictionaryReader,
  type DictEntry,
} from "../src/dictionary.ts";

function makeEntries(terms: string[]): DictEntry[] {
  return [...terms].sort().map((term, i) => ({
    term,
    postingsOffset: 1000 + i * 10,
    postingsLength: 10,
    docFreq: i + 1,
  }));
}

test("every stored term is found with its metadata", () => {
  const terms = [
    "apple", "application", "apply", "apricot", "banana", "band",
    "bandwidth", "database", "storage", "store", "zebra",
  ];
  const entries = makeEntries(terms);
  const dict = new DictionaryReader(buildDictionary(entries, 4));
  assert.equal(dict.termCount, terms.length);
  for (const entry of entries) {
    assert.deepEqual(dict.lookup(entry.term), entry);
  }
});

test("missing terms return null", () => {
  const entries = makeEntries(["apple", "banana", "cherry", "date"]);
  const dict = new DictionaryReader(buildDictionary(entries, 2));
  for (const missing of ["", "aardvark", "app", "applepie", "blueberry", "zzz"]) {
    assert.equal(dict.lookup(missing), null, `expected null for "${missing}"`);
  }
});

test("prefix compression round-trips long shared prefixes", () => {
  const terms = ["storage", "storageengine", "storageengines", "storagetier"];
  const entries = makeEntries(terms);
  const dict = new DictionaryReader(buildDictionary(entries, 8));
  for (const entry of entries) {
    assert.deepEqual(dict.lookup(entry.term), entry);
  }
});

test("block boundaries and single-block edge cases", () => {
  const many = Array.from({ length: 100 }, (_, i) => `term${String(i).padStart(4, "0")}`);
  for (const blockSize of [1, 2, 3, 7, 100]) {
    const entries = makeEntries(many);
    const dict = new DictionaryReader(buildDictionary(entries, blockSize));
    for (const entry of entries) {
      assert.deepEqual(dict.lookup(entry.term), entry, `blockSize=${blockSize}`);
    }
    assert.equal(dict.lookup("term9999"), null);
  }
});

test("empty dictionary", () => {
  const dict = new DictionaryReader(buildDictionary([]));
  assert.equal(dict.termCount, 0);
  assert.equal(dict.lookup("anything"), null);
});

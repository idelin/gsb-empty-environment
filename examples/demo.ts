import { ChunkedText, MatchIndex, findAll } from "../src/index.ts";

const show = (label: string, value: unknown) => console.log(`${label}:`, value);

// 1. Overlapping matches: "aaaa" / "aa" -> 0, 1, 2
{
  const text = new ChunkedText("aaaa", 2);
  show('"aaaa" / "aa" (chunkSize 2)', findAll(text, "aa"));
}

// 2. Pattern spanning three chunks
{
  const text = new ChunkedText("xabcdefy", 2); // xa|bc|de|fy
  show('"abcde" across 3 chunks', findAll(text, "abcde"));
}

// 3. From-offset + case sensitivity
{
  const text = new ChunkedText("aAaa", 2);
  show('"aa" case-exact', findAll(text, "aa"));
  show('"aa" case-insensitive', findAll(text, "aa", { caseSensitive: false }));
  show('"aa" from offset 2', findAll(new ChunkedText("aaaa", 2), "aa", { from: 2 }));
}

// 4. Empty pattern semantics: every position 0..length
show('empty pattern in "abc"', findAll(new ChunkedText("abc", 2), ""));

// 5. Incremental index + pausable multi-pattern cursor
{
  const text = new ChunkedText("the quick brown fox jumps over the lazy dog", 6);
  const index = new MatchIndex(text);
  index.addPattern("the", "the");
  index.addPattern("o", "o");
  index.addPattern("THE", "the", false); // case-insensitive

  const cursor = index.query(["the", "o"]);
  console.log("cursor (paused after 2):");
  console.log("  ", cursor.next().value, cursor.next().value);
  console.log("  ... paused; editing text ...");
  text.replace(4, 9, "slow"); // "the slow brown fox ..."
  console.log("  resumed after edit:");
  for (let r = cursor.next(); !r.done; r = cursor.next()) console.log("  ", r.value);

  text.charsRead = 0;
  text.replace(10, 15, "red");
  show('matches("o") after edits', index.matches("o"));
  show("chars re-read for last edit", text.charsRead);
}

// 6. Deleting a chunk boundary creates a new match
{
  const text = new ChunkedText("aabaa", 3); // aab|aa
  const index = new MatchIndex(text);
  index.addPattern("aa", "aa");
  show('"aa" in "aabaa"', index.matches("aa"));
  text.replace(2, 3, ""); // delete 'b' -> "aaaa"
  show('"aa" after deleting boundary char', index.matches("aa"));
}

// 7. Surrogate pairs and CRLF across chunks
{
  const text = new ChunkedText("a😀\r\nb😀", 2);
  show('"😀" offsets (UTF-16)', findAll(text, "😀"));
  show('"\\r\\n" offset', findAll(text, "\r\n"));
  try {
    text.replace(2, 3, "x"); // would split the pair at [1,3)
  } catch (e) {
    show("edit splitting a surrogate pair", (e as Error).message);
  }
}

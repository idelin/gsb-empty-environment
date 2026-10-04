import test from "node:test";
import assert from "node:assert/strict";
import { SentenceSegmenter, segment } from "../src/index.ts";
import type { SegmenterOptions, Sentence } from "../src/index.ts";

const OPTS: SegmenterOptions = {
  dictionaries: {
    v1: ["Dr", "Mr", "Mrs", "U.S", "e.g", "etc", "Inc"],
    v2: ["Mr"],
  },
  dictionaryVersion: "v1",
};

function texts(sentences: Sentence[]): string[] {
  return sentences.map((s) => s.text);
}

function feedChunks(chunks: string[], options: SegmenterOptions = OPTS): Sentence[] {
  const seg = new SentenceSegmenter(options);
  const out: Sentence[] = [];
  for (const chunk of chunks) out.push(...seg.push(chunk));
  out.push(...seg.flush());
  return out;
}

test("consecutive quotes split into separate sentences", () => {
  const text = 'He said "Go.""Stop." She left.';
  assert.deepEqual(texts(segment(text, OPTS)), ['He said "Go."', '"Stop."', "She left."]);
});

test("nested quotes: terminator absorbs all matching closers", () => {
  const text = 'The sign read "Do [not] enter!" We left.';
  const seg = new SentenceSegmenter({
    ...OPTS,
    quotePairs: [
      { open: '"', close: '"' },
      { open: "[", close: "]" },
    ],
  });
  const out = seg.push(text);
  out.push(...seg.flush());
  assert.deepEqual(texts(out), ['The sign read "Do [not] enter!"', "We left."]);
});

test("closing quote after terminator belongs to the same sentence", () => {
  const text = 'She replied "yes". He nodded.';
  assert.deepEqual(texts(segment(text, OPTS)), ['She replied "yes".', "He nodded."]);
});

test("prefix-overlapping dictionary entries resolve to the longest token", () => {
  const options: SegmenterOptions = {
    dictionaries: { wide: ["Mr", "Mr.Dr"], narrow: ["Mr"] },
    dictionaryVersion: "wide",
  };
  const text = "Mr.Dr. Smith left. Mr. Jones stayed. ";
  assert.deepEqual(texts(segment(text, options)), ["Mr.Dr. Smith left.", "Mr. Jones stayed."]);

  const narrow = segment(text, { ...options, dictionaryVersion: "narrow" });
  assert.deepEqual(texts(narrow), ["Mr.Dr.", "Smith left.", "Mr. Jones stayed."]);
});

test("empty segments and whitespace-only input produce nothing", () => {
  const seg = new SentenceSegmenter(OPTS);
  assert.deepEqual(seg.push(""), []);
  assert.deepEqual(seg.push("   "), []);
  assert.deepEqual(seg.push(""), []);
  assert.deepEqual(seg.flush(), []);
  assert.deepEqual(segment("", OPTS), []);
  assert.deepEqual(segment(" \n\t ", OPTS), []);
});

test("empty chunks interleaved with real input do not disturb output", () => {
  const viaEmpty = feedChunks(["", "One. ", "", "Two.", ""]);
  assert.deepEqual(texts(viaEmpty), ["One.", "Two."]);
});

test("pending region: nothing commits while the end is undetermined", () => {
  const seg = new SentenceSegmenter(OPTS);
  assert.deepEqual(seg.push("I met Dr."), [], "abbreviation period must not commit");
  assert.equal(seg.pending, "I met Dr.");
  assert.deepEqual(seg.push(" Smith"), []);
  assert.deepEqual(seg.push("."), [], "terminator at buffer end stays pending");
  assert.equal(seg.pending, "I met Dr. Smith.");
  const committed = seg.push(" He left.");
  assert.deepEqual(texts(committed), ["I met Dr. Smith."]);
  assert.deepEqual(committed[0], { text: "I met Dr. Smith.", start: 0, end: 16 });
  assert.equal(seg.pending, "He left.");
  assert.deepEqual(texts(seg.flush()), ["He left."]);
});

test("dictionary version switch affects only future text; committed ranges stay stable", () => {
  const options: SegmenterOptions = {
    dictionaries: { wide: ["Mr", "Mr.Dr"], narrow: ["Mr"] },
    dictionaryVersion: "wide",
  };
  const seg = new SentenceSegmenter(options);
  const first = seg.push("Mr.Dr. Smith left. ");
  assert.deepEqual(texts(first), ["Mr.Dr. Smith left."]);

  seg.setDictionaryVersion("narrow");
  const second = seg.push("Mr.Dr. Jones left. ");
  assert.deepEqual(texts(second), ["Mr.Dr.", "Jones left."]);
  assert.deepEqual(seg.flush(), []);

  // committed sentence keeps its original text and source range
  assert.deepEqual(first[0], { text: "Mr.Dr. Smith left.", start: 0, end: 18 });
  // later sentences carry global offsets
  assert.deepEqual(second[0], { text: "Mr.Dr.", start: 19, end: 25 });
  assert.deepEqual(second[1], { text: "Jones left.", start: 26, end: 37 });

  assert.throws(() => seg.setDictionaryVersion("nope"), /unknown dictionary version/);
});

test("char-by-char and chunked input match the batch state machine", () => {
  const text =
    'Dr. Smith said "the U.S. economy grew 2.5%..." Really?! ' +
    'Mr. Lee noted "e.g. this works." 彼は言った「行け。」次。End.';

  const expected = segment(text, OPTS);
  assert.deepEqual(texts(expected), [
    'Dr. Smith said "the U.S. economy grew 2.5%..."',
    "Really?!",
    'Mr. Lee noted "e.g. this works."',
    "彼は言った「行け。」",
    "次。",
    "End.",
  ]);

  // source ranges slice back to the original text
  for (const s of expected) {
    assert.equal(text.slice(s.start, s.end), s.text);
  }

  // one character at a time
  const charByChar: Sentence[] = [];
  const segChar = new SentenceSegmenter(OPTS);
  for (const ch of text) charByChar.push(...segChar.push(ch));
  charByChar.push(...segChar.flush());
  assert.deepEqual(charByChar, expected);

  // fixed-size chunks of 7
  const chunks: string[] = [];
  for (let i = 0; i < text.length; i += 7) chunks.push(text.slice(i, i + 7));
  assert.deepEqual(feedChunks(chunks), expected);

  // awkward splits inside abbreviations, quotes and terminators
  const cutPoints = [1, 2, 3, 4, 5, 6, 8, 9, 10, 11, 13, 17, 23, 29, 31];
  const pieces: string[] = [];
  let prev = 0;
  for (const c of cutPoints) {
    pieces.push(text.slice(prev, c));
    prev = c;
  }
  pieces.push(text.slice(prev));
  assert.deepEqual(feedChunks(pieces), expected);
});

test("trailing text without terminator is committed on flush", () => {
  assert.deepEqual(texts(segment("No terminator here", OPTS)), ["No terminator here"]);
  assert.deepEqual(texts(segment("One. Two", OPTS)), ["One.", "Two"]);
});

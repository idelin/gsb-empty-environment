import test from 'node:test';
import assert from 'node:assert/strict';
import {
  SentenceSegmenter,
  segmentAll,
  type SegmenterOptions,
  type Sentence,
} from '../src/index.ts';

const OPTIONS: SegmenterOptions = {
  dictionaries: {
    // v1 and v2 differ only in "Corp." so version switching is observable.
    v1: ['Dr.', 'Mr.', 'Mrs.', 'Corp.', 'etc.', 'U.S.', 'U.S.A.'],
    v2: ['Dr.', 'Mr.', 'Mrs.', 'etc.', 'U.S.', 'U.S.A.'],
  },
  version: 'v1',
};

function assertSpansMatch(text: string, sentences: Sentence[]): void {
  for (const s of sentences) {
    assert.equal(text.slice(s.start, s.end), s.text, `span mismatch for ${JSON.stringify(s)}`);
  }
}

function segmentStreaming(text: string, chunks: string[], options: SegmenterOptions = OPTIONS): Sentence[] {
  assert.equal(chunks.join(''), text, 'chunks must reassemble into text');
  const seg = new SentenceSegmenter(options);
  const out: Sentence[] = [];
  for (const chunk of chunks) out.push(...seg.feed(chunk));
  out.push(...seg.flush());
  return out;
}

/** Deterministic PRNG so random chunkings are reproducible. */
function lcg(seed: number): () => number {
  let state = seed;
  return () => {
    state = (state * 1103515245 + 12345) & 0x7fffffff;
    return state / 0x7fffffff;
  };
}

function randomChunks(text: string, rand: () => number): string[] {
  const chunks: string[] = [];
  let i = 0;
  while (i < text.length) {
    const size = 1 + Math.floor(rand() * 7);
    chunks.push(text.slice(i, i + size));
    i += size;
  }
  return chunks;
}

function charChunks(text: string): string[] {
  return [...text];
}

test('consecutive and nested quotes stay attached to their sentence', () => {
  const text = `She said "It's 'done.'" Twice. 'First.' 'Second.'`;
  const sentences = segmentAll(text, OPTIONS);
  assert.deepEqual(
    sentences.map((s) => s.text),
    [`She said "It's 'done.'"`, 'Twice.', `'First.'`, `'Second.'`],
  );
  assertSpansMatch(text, sentences);
});

test('consecutive closing quotes end the sentence after the outermost quote', () => {
  const text = `He said 'she wrote "Done."' Next.`;
  const sentences = segmentAll(text, OPTIONS);
  assert.deepEqual(
    sentences.map((s) => s.text),
    [`He said 'she wrote "Done."'`, 'Next.'],
  );
  assertSpansMatch(text, sentences);
});

test('dictionary prefix overlap: U.S. and U.S.A. never split mid-token', () => {
  const text = 'The U.S.A. is big. The U.S. is too. Really!';
  const expected = segmentAll(text, OPTIONS);
  assert.deepEqual(
    expected.map((s) => s.text),
    ['The U.S.A. is big.', 'The U.S. is too.', 'Really!'],
  );

  // Awkward chunk boundaries land right on the overlapping prefixes.
  const chunks = ['The U', '.', 'S', '.A', '. is big', '. The U.S', '. is too. Real', 'ly!'];
  const seg = new SentenceSegmenter(OPTIONS);
  const streamed: Sentence[] = [];
  chunks.forEach((chunk, index) => {
    const committed = seg.feed(chunk);
    // Nothing may be committed while the token could still grow.
    if (index < 5) assert.deepEqual(committed, [], `premature commit after ${JSON.stringify(chunk)}`);
    streamed.push(...committed);
  });
  streamed.push(...seg.flush());
  assert.deepEqual(streamed, expected);
});

test('empty segments: empty chunks, blank input, and blank gaps between sentences', () => {
  const seg = new SentenceSegmenter(OPTIONS);
  assert.deepEqual(seg.feed(''), []);
  assert.deepEqual(seg.feed('   '), []);
  assert.deepEqual(seg.flush(), []);

  assert.deepEqual(segmentAll('', OPTIONS), []);
  assert.deepEqual(segmentAll(' \n\t ', OPTIONS), []);

  const text = 'One.\n\n  Two.\n';
  const sentences = segmentStreaming(text, ['One.', '', '\n', '\n  T', 'wo.', '\n']);
  assert.deepEqual(
    sentences.map((s) => [s.text, s.start, s.end]),
    [
      ['One.', 0, 4],
      ['Two.', 8, 12],
    ],
  );
  assertSpansMatch(text, sentences);
});

test('pending region: nothing is committed while the ending is undecided', () => {
  const seg = new SentenceSegmenter(OPTIONS);
  assert.deepEqual(seg.feed('Hello.'), [], 'terminator at chunk end must stay pending');
  assert.deepEqual(seg.feed(' World!').map((s) => s.text), ['Hello.']);
  assert.deepEqual(seg.flush().map((s) => s.text), ['World!']);

  const abbr = new SentenceSegmenter(OPTIONS);
  assert.deepEqual(abbr.feed('Dr.'), [], 'abbreviation period must not be committed early');
  assert.deepEqual(abbr.feed(' Smith arrived.'), []);
  assert.deepEqual(abbr.flush().map((s) => s.text), ['Dr. Smith arrived.']);

  const noTerminator = new SentenceSegmenter(OPTIONS);
  noTerminator.feed('He left');
  assert.deepEqual(noTerminator.flush().map((s) => [s.text, s.start, s.end]), [['He left', 0, 7]]);
});

test('dictionary version switch affects only the pending region', () => {
  const text = 'Alpha Corp. Beta etc. Gamma.';

  // v1 knows "Corp.": the whole text is a single sentence.
  assert.deepEqual(segmentAll(text, OPTIONS).map((s) => s.text), [text]);

  // v2 dropped "Corp.": it becomes a real boundary.
  const v2 = segmentAll(text, { ...OPTIONS, version: 'v2' });
  assert.deepEqual(v2.map((s) => s.text), ['Alpha Corp.', 'Beta etc. Gamma.']);

  // Switch versions mid-stream after "Corp." was buffered under v1.
  const seg = new SentenceSegmenter(OPTIONS);
  assert.deepEqual(seg.feed('Alpha Corp. '), [], 'abbreviation stays pending under v1');
  seg.setDictionaryVersion('v2');
  const committed = seg.feed('Beta etc. Gamma.');
  assert.deepEqual(committed.map((s) => [s.text, s.start, s.end]), [['Alpha Corp.', 0, 11]]);
  const tail = seg.flush();
  assert.deepEqual(tail.map((s) => [s.text, s.start, s.end]), [['Beta etc. Gamma.', 12, 28]]);

  // Committed spans are stable: further feeds and version switches never rewrite them.
  seg.setDictionaryVersion('v1');
  seg.feed('Delta.');
  seg.flush();
  assert.deepEqual([...committed, ...tail].map((s) => [s.text, s.start, s.end]), [
    ['Alpha Corp.', 0, 11],
    ['Beta etc. Gamma.', 12, 28],
  ]);

  assert.throws(() => seg.setDictionaryVersion('v3'), /unknown dictionary version/);
});

const CORPUS: string[] = [
  'Dr. Smith left. He said "Bye." OK!',
  'The U.S.A. is big. Price is 3.14 today. Wow?! Yes.',
  `She said "It's 'done.'" Then 'gone.'`,
  'No terminator at the end',
  'Alpha Corp. Beta etc. Gamma.',
  '"One. Two." Three.',
  'Mr. and Mrs. U.S. citizens, etc. all left. Done',
];

test('char-by-char streaming matches the batch state machine', () => {
  for (const text of CORPUS) {
    const expected = segmentAll(text, OPTIONS);
    const streamed = segmentStreaming(text, charChunks(text));
    assert.deepEqual(streamed, expected, `char-by-char mismatch for ${JSON.stringify(text)}`);
    assertSpansMatch(text, streamed);
  }
});

test('random chunkings match the batch state machine', () => {
  for (const text of CORPUS) {
    const expected = segmentAll(text, OPTIONS);
    const rand = lcg(42);
    for (let round = 0; round < 25; round++) {
      const streamed = segmentStreaming(text, randomChunks(text, rand));
      assert.deepEqual(streamed, expected, `chunked mismatch (round ${round}) for ${JSON.stringify(text)}`);
    }
  }
});

test('committed sentence objects are never mutated retroactively', () => {
  const seg = new SentenceSegmenter(OPTIONS);
  const first = seg.feed('One. Two. ');
  assert.deepEqual(first.map((s) => s.text), ['One.', 'Two.']);
  const snapshot = JSON.parse(JSON.stringify(first)) as Sentence[];
  seg.setDictionaryVersion('v2');
  seg.feed('Three.');
  seg.flush();
  assert.deepEqual(first, snapshot, 'previously committed sentences must stay stable');
});

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BitReader, BitWriter } from '../src/bitstream.ts';
import {
  assignCanonicalCodes,
  buildCodeLengths,
  HuffmanDecoder,
} from '../src/huffman.ts';
import {
  ALPHABET_SIZE,
  BLACK_TABLE,
  ESCAPE_SYMBOL,
  WHITE_TABLE,
} from '../src/tables.ts';

test('preset tables assign a code to every symbol in the alphabet', () => {
  for (const table of [WHITE_TABLE, BLACK_TABLE]) {
    assert.equal(table.codes.length, ALPHABET_SIZE);
    for (let s = 0; s < ALPHABET_SIZE; s++) {
      assert.ok(table.codes[s].length > 0, `symbol ${s} has no code`);
      assert.ok(table.codes[s].length <= 30, `symbol ${s} code too long`);
    }
  }
});

test('preset tables are prefix-free', () => {
  for (const table of [WHITE_TABLE, BLACK_TABLE]) {
    const codes = table.codes;
    for (let i = 0; i < codes.length; i++) {
      for (let j = 0; j < codes.length; j++) {
        if (i === j) continue;
        const a = codes[i];
        const b = codes[j];
        if (a.length > b.length) continue;
        const prefix = b.code >>> (b.length - a.length);
        assert.notEqual(
          prefix,
          a.code,
          `code of symbol ${i} is a prefix of symbol ${j}`,
        );
      }
    }
  }
});

test('every symbol round-trips through the decoder tree', () => {
  for (const table of [WHITE_TABLE, BLACK_TABLE]) {
    const writer = new BitWriter();
    for (let s = 0; s < ALPHABET_SIZE; s++) {
      writer.writeBits(table.codes[s].code, table.codes[s].length);
    }
    const reader = new BitReader(writer.toUint8Array());
    for (let s = 0; s < ALPHABET_SIZE; s++) {
      assert.equal(table.decoder.decode(reader), s);
    }
  }
});

test('canonical assignment is deterministic and ordered', () => {
  const lengths = buildCodeLengths([5, 5, 1, 1, 2, 2]);
  const first = assignCanonicalCodes(lengths);
  const second = assignCanonicalCodes(buildCodeLengths([5, 5, 1, 1, 2, 2]));
  assert.deepEqual(first, second);
  const sorted = [...first].sort((a, b) => a.length - b.length);
  for (let i = 1; i < sorted.length; i++) {
    assert.ok(sorted[i].code > sorted[i - 1].code || sorted[i].length > sorted[i - 1].length);
  }
});

test('decoder rejects invalid bit sequences', () => {
  const lengths = new Uint8Array([1, 1]);
  const codes = assignCanonicalCodes(lengths);
  const decoder = new HuffmanDecoder(codes);
  // alphabet has only codes 0 and 1; a reader over empty data must throw
  const reader = new BitReader(new Uint8Array(0));
  assert.throws(() => decoder.decode(reader));
});

test('escape symbol exists in both tables', () => {
  assert.ok(WHITE_TABLE.codes[ESCAPE_SYMBOL].length > 0);
  assert.ok(BLACK_TABLE.codes[ESCAPE_SYMBOL].length > 0);
});

import assert from 'node:assert/strict';
import {
  StreamDecoder,
  decode,
  decodeBytes,
  decodeUtf8,
  encode,
  encodeUtf8,
  DEFAULT_MAX_RAW_LEN,
} from '../build/codec.js';

let passed = 0;
let failed = 0;

function test(name, fn) {
  try {
    fn();
    passed += 1;
    console.log('ok   ' + name);
  } catch (err) {
    failed += 1;
    console.error('FAIL ' + name);
    console.error('  ' + String(err && err.message).split('\n').join('\n  '));
  }
}

function ascii(str) {
  return Uint8Array.from([...str].map((ch) => ch.charCodeAt(0)));
}

function concat(...parts) {
  const total = parts.reduce((sum, p) => sum + p.length, 0);
  const out = new Uint8Array(total);
  let off = 0;
  for (const p of parts) {
    out.set(p, off);
    off += p.length;
  }
  return out;
}

function collect(decoder, chunks) {
  const spans = [];
  for (const chunk of chunks) spans.push(...decoder.push(chunk));
  spans.push(...decoder.end());
  return spans;
}

function bytesOf(spans) {
  return spans.map((s) => s.byte);
}

function latin1(spans) {
  return String.fromCharCode(...bytesOf(spans));
}

function mulberry32(seed) {
  return function () {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function assertSpansSane(spans, inputLen) {
  let prevStart = -1;
  for (const s of spans) {
    assert.ok(Number.isInteger(s.start) && Number.isInteger(s.end), 'integer offsets');
    assert.ok(s.start > prevStart, 'strictly increasing');
    assert.ok(s.start < s.end, 'non-empty range');
    assert.ok(s.start >= 0 && s.end <= inputLen, 'within input');
    prevStart = s.start;
  }
}

// ---- exact span mapping ------------------------------------------------

test('per-byte input ranges for escapes and literals', () => {
  assert.deepEqual(decode(ascii('a\\x41b')), [
    { byte: 0x61, start: 0, end: 1 },
    { byte: 0x41, start: 1, end: 5 },
    { byte: 0x62, start: 5, end: 6 },
  ]);
  assert.deepEqual(decode(ascii('\\\\')), [{ byte: 0x5c, start: 0, end: 2 }]);
  assert.deepEqual(decode(ascii('\\r3:abc')), [
    { byte: 0x61, start: 4, end: 5 },
    { byte: 0x62, start: 5, end: 6 },
    { byte: 0x63, start: 6, end: 7 },
  ]);
});

test('absolute input ranges accumulate across pushes', () => {
  const decoder = new StreamDecoder();
  assert.deepEqual(decoder.push(ascii('ab')), [
    { byte: 0x61, start: 0, end: 1 },
    { byte: 0x62, start: 1, end: 2 },
  ]);
  assert.deepEqual(decoder.push(ascii('\\x41')), [{ byte: 0x41, start: 2, end: 6 }]);
  assert.deepEqual(decoder.push(ascii('\\r2:xy')), [
    { byte: 0x78, start: 10, end: 11 },
    { byte: 0x79, start: 11, end: 12 },
  ]);
  assert.deepEqual(decoder.end(), []);
});

// ---- escapes & raw blocks ----------------------------------------------

test('hex escapes are case-insensitive', () => {
  assert.deepEqual(bytesOf(decode(ascii('\\x4a\\x4A\\xfF\\x00'))), [0x4a, 0x4a, 0xff, 0x00]);
});

test('backslash has no special meaning inside raw blocks', () => {
  assert.equal(latin1(decode(ascii('\\r3:a\\\\'))), 'a\\\\');
  assert.equal(latin1(decode(ascii('\\r4:\\x41'))), '\\x41');
  assert.equal(latin1(decode(ascii('\\r2:\\rx'))), '\\rx');
  assert.equal(latin1(decode(ascii('\\r0:'))), '');
  assert.equal(latin1(decode(ascii('\\r007:1234567'))), '1234567');
});

test('invalid escapes do not swallow following valid input', () => {
  assert.equal(latin1(decode(ascii('\\q\\x41'))), '\\qA');
  assert.equal(latin1(decode(ascii('\\xZZ\\x41'))), '\\xZZA');
  assert.equal(latin1(decode(ascii('\\x4G\\x41'))), '\\x4GA');
  assert.equal(latin1(decode(ascii('\\rx\\x41'))), '\\rxA');
  assert.equal(latin1(decode(ascii('\\r\\x41'))), '\\rA');
  assert.equal(latin1(decode(ascii('\\r:\\x41'))), '\\r:A');
});

test('raw length limit', () => {
  assert.equal(latin1(collect(new StreamDecoder({ maxRawLen: 3 }), [ascii('\\r3:abc')])), 'abc');
  assert.equal(latin1(collect(new StreamDecoder({ maxRawLen: 3 }), [ascii('\\r4:abcd')])), '\\r4:abcd');
  assert.equal(latin1(collect(new StreamDecoder({ maxRawLen: 0 }), [ascii('\\r0:')])), '');
  assert.equal(latin1(collect(new StreamDecoder({ maxRawLen: 0 }), [ascii('\\r1:a')])), '\\r1:a');
  assert.equal(latin1(decode(ascii('\\r999999999999:x'))), '\\r999999999999:x');
  const over = '\\r' + (DEFAULT_MAX_RAW_LEN + 1) + ':x';
  assert.equal(latin1(decode(ascii(over))), over);
  assert.throws(() => new StreamDecoder({ maxRawLen: -1 }), RangeError);
});

test('truncated sequences pass through literally on end()', () => {
  const cases = ['\\', '\\x', '\\x4', '\\r', '\\r1', '\\r12', '\\r12:', '\\r5:ab', '\\r5:abc'];
  for (const t of cases) {
    assert.deepEqual(bytesOf(decode(ascii(t))), [...t].map((c) => c.charCodeAt(0)), JSON.stringify(t));
  }
});

test('push after end throws', () => {
  const decoder = new StreamDecoder();
  decoder.end();
  assert.throws(() => decoder.push(ascii('a')));
});

// ---- chunk-split invariance ---------------------------------------------

const corpus = [
  ['empty', ascii('')],
  ['plain', ascii('hello world')],
  ['escaped backslash', ascii('\\\\')],
  ['hex escapes', ascii('\\x41\\x42\\x43')],
  ['hex mixed case', ascii('\\x4a\\x4A\\xfF')],
  ['hex around literals', ascii('a\\x41b')],
  ['invalid then valid', ascii('\\q\\x41')],
  ['bad hex then valid', ascii('\\xZZ\\x41')],
  ['one hex digit then junk', ascii('\\x4Gz')],
  ['raw block', ascii('\\r5:hello')],
  ['raw block empty', ascii('\\r0:')],
  ['raw block zero-padded', ascii('\\r007:1234567')],
  ['raw block with backslashes', ascii('\\r3:a\\\\')],
  ['raw block hides escape', ascii('\\r4:\\x41')],
  ['raw block hides raw prefix', ascii('\\r2:\\rx')],
  ['raw then text', ascii('x\\r2:aby')],
  ['truncated backslash', ascii('\\')],
  ['truncated hex', ascii('\\x4')],
  ['truncated raw header', ascii('\\r12')],
  ['truncated raw colon', ascii('\\r12:')],
  ['truncated raw payload', ascii('\\r5:ab')],
  ['utf8 via hex', ascii('caf\\xC3\\xA9')],
  ['mixed everything', ascii('pre\\x00\\r3:xyz\\qend\\\\')],
  ['binary raw payload', concat(ascii('\\r4:'), Uint8Array.of(0x00, 0x5c, 0xff, 0xc3))],
];

function checkAllSplits(input, label) {
  const expected = collect(new StreamDecoder(), [input]);
  test(label + ': byte-by-byte equals one-shot', () => {
    const chunks = [];
    for (let k = 0; k < input.length; k += 1) chunks.push(input.slice(k, k + 1));
    assert.deepEqual(collect(new StreamDecoder(), chunks), expected);
  });
  test(label + ': all two-chunk splits', () => {
    for (let k = 0; k <= input.length; k += 1) {
      assert.deepEqual(
        collect(new StreamDecoder(), [input.slice(0, k), input.slice(k)]),
        expected,
        'split at ' + k,
      );
    }
  });
  if (input.length <= 24) {
    test(label + ': all three-chunk splits', () => {
      for (let a = 0; a <= input.length; a += 1) {
        for (let b = a; b <= input.length; b += 1) {
          assert.deepEqual(
            collect(new StreamDecoder(), [input.slice(0, a), input.slice(a, b), input.slice(b)]),
            expected,
            'split at ' + a + ',' + b,
          );
        }
      }
    });
  }
  test(label + ': empty chunks interleaved', () => {
    assert.deepEqual(
      collect(new StreamDecoder(), [new Uint8Array(0), input, new Uint8Array(0)]),
      expected,
    );
  });
}

for (const [label, input] of corpus) checkAllSplits(input, label);

// ---- UTF-8 validated only after decoding ---------------------------------

test('utf-8 is validated after decoding', () => {
  assert.equal(decodeUtf8(ascii('caf\\xC3\\xA9')), 'café');
  assert.equal(decodeUtf8(ascii('\\xE2\\x82\\xAC')), '€');
  assert.equal(decodeUtf8(concat(ascii('\\r2:'), Uint8Array.of(0xc3, 0xa9))), 'é');
  assert.equal(
    decodeUtf8(concat(ascii('\\r1:'), Uint8Array.of(0xe2), ascii('\\x82\\xAC'))),
    '€',
  );
});

test('utf-8 multibyte sequence assembled across chunk boundaries', () => {
  const input = concat(ascii('a\\xC3'), Uint8Array.of(0xa9), ascii('\\xE2\\x82\\xACz'));
  const decoder = new StreamDecoder();
  const spans = [];
  for (let k = 0; k < input.length; k += 1) spans.push(...decoder.push(input.slice(k, k + 1)));
  spans.push(...decoder.end());
  const text = new TextDecoder('utf-8', { fatal: true }).decode(Uint8Array.from(bytesOf(spans)));
  assert.equal(text, 'aé€z');
});

test('invalid utf-8 is rejected only at validation stage', () => {
  assert.deepEqual(bytesOf(decode(ascii('\\xFF'))), [0xff]);
  assert.deepEqual(bytesOf(decode(ascii('\\xC3'))), [0xc3]);
  assert.throws(() => decodeUtf8(ascii('\\xFF')));
  assert.throws(() => decodeUtf8(ascii('\\xC3')));
  assert.throws(() => decodeUtf8(ascii('\\xC3(')));
});

test('encodeUtf8/decodeUtf8 roundtrip', () => {
  const samples = ['', 'hello', 'héllo €', '中文测试', 'back\\slash', 'mix\\x41ed € \\'];
  for (const s of samples) assert.equal(decodeUtf8(encodeUtf8(s)), s);
});

// ---- encoder / roundtrip --------------------------------------------------

test('encode/decode roundtrip (random buffers, all thresholds)', () => {
  const rand = mulberry32(0xc0ffee);
  for (const size of [0, 1, 2, 3, 4, 5, 7, 16, 64, 300]) {
    for (const rawThreshold of [0, 1, 4, Infinity]) {
      const data = Uint8Array.from({ length: size }, () => Math.floor(rand() * 256));
      const encoded = encode(data, { rawThreshold });
      const spans = decode(encoded);
      assert.deepEqual(Uint8Array.from(bytesOf(spans)), data, `size=${size} threshold=${rawThreshold}`);
      assertSpansSane(spans, encoded.length);
    }
  }
});

test('roundtrip streaming byte-by-byte', () => {
  const rand = mulberry32(42);
  const data = Uint8Array.from({ length: 200 }, () => Math.floor(rand() * 256));
  const encoded = encode(data);
  const decoder = new StreamDecoder();
  const spans = [];
  for (let k = 0; k < encoded.length; k += 1) spans.push(...decoder.push(encoded.slice(k, k + 1)));
  spans.push(...decoder.end());
  assert.deepEqual(Uint8Array.from(bytesOf(spans)), data);
});

test('roundtrip all byte values and backslash runs', () => {
  const all = Uint8Array.from({ length: 256 }, (_, i) => i);
  assert.deepEqual(decodeBytes(encode(all)), all);
  const slashes = Uint8Array.from({ length: 50 }, () => 0x5c);
  assert.deepEqual(decodeBytes(encode(slashes)), slashes);
});

test('encoder uses raw blocks for long unsafe runs', () => {
  const encoded = encode(Uint8Array.from({ length: 10 }, () => 0));
  assert.equal(encoded.length, 5 + 10);
  assert.equal(latin1(decode(encoded)), '\0'.repeat(10));
});

test('encoded output is printable ASCII when raw blocks disabled', () => {
  const rand = mulberry32(7);
  const data = Uint8Array.from({ length: 100 }, () => Math.floor(rand() * 256));
  const encoded = encode(data, { rawThreshold: Infinity });
  for (const b of encoded) assert.ok(b >= 0x20 && b <= 0x7e);
});

console.log(`\n${passed} passed, ${failed} failed`);
process.exitCode = failed === 0 ? 0 : 1;

import {
  decode,
  decodeToBytes,
  encode,
  EscapeDecoder,
  Utf8StringDecoder,
  DecodedByte,
  DEFAULT_MAX_RAW_LENGTH,
} from "../src/codec";

declare const process: { exit(code: number): void };

// ---------- tiny test harness ----------
let passed = 0;
let failed = 0;
function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log(`ok   ${name}`);
  } catch (err) {
    failed++;
    console.error(`FAIL ${name}`);
    console.error(err instanceof Error && err.stack ? err.stack : err);
  }
}
function assert(cond: boolean, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}
function assertEq<T>(actual: T, expected: T, msg: string): void {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  if (a !== e) throw new Error(`${msg}\n  expected: ${e}\n  actual:   ${a}`);
}

// ---------- helpers ----------
function ascii(s: string): Uint8Array {
  const b = new Uint8Array(s.length);
  for (let i = 0; i < s.length; i++) b[i] = s.charCodeAt(i);
  return b;
}
function latin1(bytes: Uint8Array): string {
  let s = "";
  for (const b of bytes) s += String.fromCharCode(b);
  return s;
}
function utf8(s: string): Uint8Array {
  return new TextEncoder().encode(s);
}
/** Decode `input` feeding it in chunks split at the given cut positions. */
function decodeChunked(input: Uint8Array, cuts: number[], maxRawLength?: number): DecodedByte[] {
  const d = new EscapeDecoder(maxRawLength === undefined ? undefined : { maxRawLength });
  let out: DecodedByte[] = [];
  let prev = 0;
  for (const cut of cuts) {
    out = out.concat(d.push(input.subarray(prev, cut)));
    prev = cut;
  }
  out = out.concat(d.push(input.subarray(prev)));
  return out.concat(d.end());
}
function bytesOf(decoded: DecodedByte[]): number[] {
  return decoded.map((d) => d.byte);
}
function rangesOf(decoded: DecodedByte[]): Array<[number, number]> {
  return decoded.map((d) => [d.start, d.end]);
}
/** Invariant: ranges are ordered, non-overlapping, within [0, input.length]. */
function assertRangesSane(decoded: DecodedByte[], inputLength: number): void {
  let prevEnd = 0;
  for (const d of decoded) {
    assert(d.start >= prevEnd, `ranges overlap or are unordered at ${d.start}`);
    assert(d.end > d.start, `empty range at ${d.start}`);
    assert(d.end <= inputLength, `range end ${d.end} beyond input length ${inputLength}`);
    prevEnd = d.end;
  }
}

// ---------- basic escapes ----------
test("named escapes and passthrough", () => {
  const d = decode(ascii("a\\n\\r\\t\\0\\\\b"));
  assertEq(latin1(Uint8Array.from(bytesOf(d))), "a\n\r\t\0\\b", "decoded bytes");
  assertRangesSane(d, 12);
});

test("hex escapes, both cases", () => {
  assertEq(latin1(decodeToBytes(ascii("\\x41\\x5a\\x61\\x7A"))), "AZaz", "hex decode");
  assertEq(bytesOf(decode(ascii("\\x00\\xff"))), [0, 255], "hex bounds");
});

test("output byte ranges map back to input", () => {
  const d = decode(ascii("a\\x41b"));
  assertEq(bytesOf(d), [0x61, 0x41, 0x62], "bytes");
  assertEq(rangesOf(d), [[0, 1], [1, 5], [5, 6]], "ranges");
});

test("ranges are absolute across pushed chunks", () => {
  const d = new EscapeDecoder();
  const out = d.push(ascii("xy")).concat(d.push(ascii("\\x41"))).concat(d.end());
  assertEq(rangesOf(out), [[0, 1], [1, 2], [2, 6]], "absolute ranges");
});

// ---------- invalid escapes ----------
test("unknown escape char does not swallow following input", () => {
  assertEq(latin1(decodeToBytes(ascii("\\q"))), "\\q", "unknown escape is literal");
  assertEq(latin1(decodeToBytes(ascii("\\q\\x41"))), "\\qA", "valid escape after invalid one");
});

test("bad hex digit falls back to literal and rescan", () => {
  assertEq(latin1(decodeToBytes(ascii("\\x4g"))), "\\x4g", "bad second hex digit");
  assertEq(latin1(decodeToBytes(ascii("\\xg1"))), "\\xg1", "bad first hex digit");
  assertEq(latin1(decodeToBytes(ascii("\\x4\\x41"))), "\\x4A", "following valid escape still decoded");
});

test("lone backslash at end of stream", () => {
  assertEq(latin1(decodeToBytes(ascii("abc\\"))), "abc\\", "trailing backslash literal");
});

test("truncated hex escape flushed literally at end", () => {
  assertEq(latin1(decodeToBytes(ascii("\\x"))), "\\x", "lone \\x");
  assertEq(latin1(decodeToBytes(ascii("\\x4"))), "\\x4", "one hex digit");
});

// ---------- raw blocks ----------
test("raw block: backslash has no special meaning", () => {
  const d = decode(ascii("\\R4:a\\nb"));
  assertEq(latin1(Uint8Array.from(bytesOf(d))), "a\\nb", "raw bytes literal");
  assertEq(rangesOf(d), [[4, 5], [5, 6], [6, 7], [7, 8]], "raw byte ranges");
});

test("empty raw block decodes to nothing", () => {
  assertEq(latin1(decodeToBytes(ascii("a\\R0:b"))), "ab", "empty raw block");
  assertEq(latin1(decodeToBytes(ascii("\\R00:"))), "", "zero-padded empty raw block");
});

test("raw block with leading-zero length", () => {
  assertEq(latin1(decodeToBytes(ascii("\\R003:abc"))), "abc", "leading zeros");
});

test("raw block length limit enforced", () => {
  // default limit
  const tooBig = `\\R${DEFAULT_MAX_RAW_LENGTH + 1}:x`;
  assertEq(latin1(decodeToBytes(ascii(tooBig))), tooBig, "over-limit length is literal");
  // custom limit
  const opts = { maxRawLength: 4 };
  assertEq(latin1(decodeToBytes(ascii("\\R4:abcd"), opts)), "abcd", "at limit ok");
  assertEq(latin1(decodeToBytes(ascii("\\R5:abcde"), opts)), "\\R5:abcde", "over limit literal");
  // digit runaway guard: absurd length stays literal, doesn't hang
  const huge = "\\R999999999999999999999:";
  assertEq(latin1(decodeToBytes(ascii(huge))), huge, "huge length literal");
});

test("raw block content can contain anything, including \\R and \\x", () => {
  const body = "\\R3:\\x41\\";
  const input = `\\R${body.length}:${body}`;
  assertEq(latin1(decodeToBytes(ascii(input))), body, "nested-looking content is literal");
});

test("truncated raw block flushed literally at end", () => {
  assertEq(latin1(decodeToBytes(ascii("\\R5:ab"))), "\\R5:ab", "short body");
  assertEq(latin1(decodeToBytes(ascii("\\R5:"))), "\\R5:", "empty body");
  assertEq(latin1(decodeToBytes(ascii("\\R5"))), "\\R5", "missing colon");
  assertEq(latin1(decodeToBytes(ascii("\\R"))), "\\R", "missing length");
  assertEq(latin1(decodeToBytes(ascii("\\R12"))), "\\R12", "partial header");
});

test("invalid raw header does not swallow following valid escape", () => {
  assertEq(latin1(decodeToBytes(ascii("\\Rx\\x41"))), "\\RxA", "non-digit after R");
  assertEq(latin1(decodeToBytes(ascii("\\R:\\x41"))), "\\R:A", "empty length");
});

// ---------- chunking: every split position ----------
const CHUNK_CASES: Array<[string, Uint8Array]> = [
  ["escapes", ascii("a\\n\\x41\\\\b\\R3:xyzc")],
  ["invalid escapes", ascii("\\q\\x4g\\x\\R\\R:\\x41")],
  ["raw with backslashes", ascii("\\R5:a\\x\\b\\n")],
  ["truncated raw", ascii("pre\\R7:ab")],
  ["utf8 via hex", ascii("\\xE4\\xB8\\xAD\\xE6\\x96\\x87")],
  ["mixed", ascii("\\R2:ab\\x41\\R0:\\t\\\\z")],
];

for (const [name, input] of CHUNK_CASES) {
  test(`chunking: every single split position (${name})`, () => {
    const expected = decode(input);
    for (let cut = 0; cut <= input.length; cut++) {
      assertEq(decodeChunked(input, [cut]), expected, `split at ${cut}`);
    }
  });

  test(`chunking: byte by byte (${name})`, () => {
    const expected = decode(input);
    const cuts: number[] = [];
    for (let i = 1; i < input.length; i++) cuts.push(i);
    assertEq(decodeChunked(input, cuts), expected, "byte-by-byte");
  });

  test(`chunking: every pair of split positions (${name})`, () => {
    const expected = decode(input);
    for (let a = 0; a <= input.length; a++) {
      for (let b = a; b <= input.length; b++) {
        assertEq(decodeChunked(input, [a, b]), expected, `splits at ${a},${b}`);
      }
    }
  });
}

test("chunking: ranges sane for all split positions", () => {
  const input = ascii("a\\x41\\R3:xyz\\q\\");
  for (let cut = 0; cut <= input.length; cut++) {
    assertRangesSane(decodeChunked(input, [cut]), input.length);
  }
});

// ---------- UTF-8 validated only after decoding ----------
test("utf8 built from hex escapes decodes as text", () => {
  const s = new Utf8StringDecoder();
  let text = "";
  const input = ascii("\\xE4\\xB8\\xAD\\xE6\\x96\\x87"); // "中文"
  for (const b of input) text += s.push(Uint8Array.of(b));
  text += s.end();
  assertEq(text, "中文", "utf8 from hex escapes");
});

test("utf8 split across chunk boundaries inside raw block", () => {
  const raw = utf8("héllo→世界");
  const header = ascii(`\\R${raw.length}:`);
  const input = new Uint8Array(header.length + raw.length);
  input.set(header, 0);
  input.set(raw, header.length);
  // split at every byte position; text must always come out identical
  for (let cut = 0; cut <= input.length; cut++) {
    const s = new Utf8StringDecoder();
    const text = s.push(input.subarray(0, cut)) + s.push(input.subarray(cut)) + s.end();
    assertEq(text, "héllo→世界", `split at ${cut}`);
  }
});

test("invalid utf8 passes byte decode but fails string decode", () => {
  const input = ascii("\\xFF\\xFE");
  assertEq(bytesOf(decode(input)), [0xff, 0xfe], "byte decode is fine");
  const s = new Utf8StringDecoder();
  let threw = false;
  try {
    s.push(input);
    s.end();
  } catch {
    threw = true;
  }
  assert(threw, "string decode must reject invalid utf8");
});

test("incomplete utf8 sequence at end throws on flush", () => {
  const s = new Utf8StringDecoder();
  s.push(ascii("\\xE4\\xB8")); // first two bytes of 中
  let threw = false;
  try {
    s.end();
  } catch {
    threw = true;
  }
  assert(threw, "truncated utf8 must throw at end()");
});

// ---------- encoder / round trips ----------
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test("round trip: escape mode, random data, byte-by-byte decode", () => {
  const rand = mulberry32(42);
  for (let trial = 0; trial < 200; trial++) {
    const len = Math.floor(rand() * 200);
    const data = new Uint8Array(len);
    for (let i = 0; i < len; i++) data[i] = Math.floor(rand() * 256);
    const encoded = encode(data);
    // escape mode output must be pure ASCII
    for (const b of encoded) assert(b < 0x80, "escape-mode output must be ASCII");
    const cuts: number[] = [];
    for (let i = 1; i < encoded.length; i++) cuts.push(i);
    assertEq(bytesOf(decodeChunked(encoded, cuts)), Array.from(data), `trial ${trial}`);
  }
});

test("round trip: raw-block mode, random data, every split position", () => {
  const rand = mulberry32(7);
  for (let trial = 0; trial < 50; trial++) {
    const len = Math.floor(rand() * 100);
    const data = new Uint8Array(len);
    for (let i = 0; i < len; i++) data[i] = Math.floor(rand() * 256);
    const encoded = encode(data, { useRawBlocks: true, maxRawLength: 16 });
    for (let cut = 0; cut <= encoded.length; cut++) {
      assertEq(bytesOf(decodeChunked(encoded, [cut], 16)), Array.from(data), `trial ${trial} cut ${cut}`);
    }
  }
});

test("round trip: utf8 text through string decoder", () => {
  const texts = ["", "hello", "中文测试", "emoji 🎉 mix \\ and \n controls \0"];
  for (const t of texts) {
    const encoded = encode(utf8(t));
    const s = new Utf8StringDecoder();
    let out = "";
    for (const b of encoded) out += s.push(Uint8Array.of(b));
    out += s.end();
    assertEq(out, t, `round trip ${JSON.stringify(t)}`);
  }
});

test("round trip: empty input", () => {
  assertEq(encode(Uint8Array.of()).length, 0, "empty encode");
  assertEq(decode(Uint8Array.of()).length, 0, "empty decode");
  assertEq(encode(Uint8Array.of(), { useRawBlocks: true }).length, 0, "empty raw encode");
});

test("encode respects maxRawLength chunking", () => {
  const data = new Uint8Array(40);
  for (let i = 0; i < 40; i++) data[i] = i;
  const encoded = encode(data, { useRawBlocks: true, maxRawLength: 16 });
  // expect headers \R16: \R16: \R8:
  assertEq(latin1(encoded.subarray(0, 5)), "\\R16:", "first header");
  assertEq(latin1(decodeToBytes(encoded, { maxRawLength: 16 })), latin1(data), "decode matches");
});

// ---------- summary ----------
console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);

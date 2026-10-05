import {
  countScalars,
  decodeUtf8,
  encodeUtf8,
  isValidUtf8,
  sliceScalars,
  utf8ByteLength,
  validateString,
  InvalidEncodingError,
} from "../src/index";
import { assert, assertEq, makeRng, textDecoder, textEncoder } from "./helpers";

function bytesEq(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}

export const tests: Array<[string, () => void]> = [
  ["encode/decode matches platform codecs", () => {
    const samples = [
      "",
      "hello world",
      "中文测试",
      "😀🎉𝄞👨‍👩‍👧‍👦",
      "éèê ᾯ ᾳ",
      "a😀中éb",
      "􏿿", // U+10FFFF
      "\uD7FF\uE000", // around the surrogate gap
    ];
    for (const s of samples) {
      const encoded = encodeUtf8(s);
      assert(bytesEq(encoded, textEncoder.encode(s) as Uint8Array), "encodeUtf8 matches TextEncoder for " + JSON.stringify(s));
      assertEq(decodeUtf8(encoded), s, "decode round-trip");
      assert(isValidUtf8(encoded), "own encoding is valid");
      assertEq(utf8ByteLength(s), encoded.length, "byte length");
      assertEq(countScalars(s), Array.from(s).length, "scalar count");
    }
  }],

  ["random strings round-trip", () => {
    const rng = makeRng(12345);
    const pool = "abé中😀🎉𝄞ẞᾯ\u0000\u007F\u0080\u07FF\u0800\uFFFF\uD7FF\uE000";
    const poolArr = Array.from(pool);
    for (let iter = 0; iter < 500; iter++) {
      const n = Math.floor(rng() * 40);
      let s = "";
      for (let i = 0; i < n; i++) s += poolArr[Math.floor(rng() * poolArr.length)];
      const enc = encodeUtf8(s);
      assertEq(decodeUtf8(enc), s, "round-trip " + JSON.stringify(s));
      assert(bytesEq(enc, textEncoder.encode(s) as Uint8Array), "encode matches platform");
      assertEq(utf8ByteLength(s), (textEncoder.encode(s) as Uint8Array).length, "byte length");
    }
  }],

  ["random byte strings: validity agrees with platform decoder", () => {
    const rng = makeRng(999);
    for (let iter = 0; iter < 2000; iter++) {
      const n = Math.floor(rng() * 8);
      const bytes = new Uint8Array(n);
      for (let i = 0; i < n; i++) {
        // bias towards lead/continuation bytes to hit interesting cases
        const r = rng();
        bytes[i] = r < 0.4 ? 0x80 + Math.floor(rng() * 0x40)
          : r < 0.7 ? 0xc0 + Math.floor(rng() * 0x40)
          : Math.floor(rng() * 256);
      }
      let platformOk = true;
      try {
        textDecoder.decode(bytes);
      } catch (_) {
        platformOk = false;
      }
      assertEq(isValidUtf8(bytes), platformOk, "validity of [" + Array.from(bytes).join(",") + "]");
      if (platformOk) {
        assertEq(decodeUtf8(bytes), textDecoder.decode(bytes), "decode agrees");
      } else {
        let threw = false;
        try {
          decodeUtf8(bytes);
        } catch (e) {
          threw = e instanceof InvalidEncodingError;
        }
        assert(threw, "decodeUtf8 throws on invalid input");
      }
    }
  }],

  ["sliceScalars respects scalar boundaries", () => {
    const s = "a😀中é𝄞b";
    const scalars = Array.from(s);
    assertEq(scalars.length, 6, "sample scalar count");
    for (let i = 0; i <= 6; i++) {
      for (let j = i; j <= 6; j++) {
        assertEq(sliceScalars(s, i, j), scalars.slice(i, j).join(""), "slice [" + i + "," + j + ")");
      }
    }
    // slicing off a lone half of a surrogate pair is impossible by construction
    assertEq(sliceScalars("😀", 0, 1), "😀", "whole emoji");
    assertEq(sliceScalars("😀", 0, 0), "", "empty slice");
    assertEq(sliceScalars("😀", 1, 1), "", "empty slice at end");
  }],

  ["validateString rejects lone surrogates only", () => {
    validateString("");
    validateString("plain ascii");
    validateString("😀🎉 中 é");
    const bad = ["\uD800", "\uDBFF", "\uDC00", "\uDFFF", "x\uD800", "\uDC00y", "a\uD800\uD800b"];
    for (const s of bad) {
      let threw = false;
      try {
        validateString(s);
      } catch (e) {
        threw = e instanceof InvalidEncodingError;
      }
      assert(threw, "rejects " + JSON.stringify(s));
    }
  }],
];

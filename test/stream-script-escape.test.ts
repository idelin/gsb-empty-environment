import test from "node:test";
import assert from "node:assert/strict";
import {
  StreamingScriptJsonEscaper,
  escapeInlineScriptJson,
} from "../index.ts";

const LS = String.fromCharCode(0x2028);
const PS = String.fromCharCode(0x2029);

const encoder = new TextEncoder();

function toBytes(text: string): Uint8Array {
  return encoder.encode(text);
}

function runStreaming(chunks: Uint8Array[]): string {
  const escaper = new StreamingScriptJsonEscaper();
  let out = "";
  for (const chunk of chunks) {
    out += escaper.push(chunk);
  }
  return out + escaper.end();
}

function splitAtEveryByte(bytes: Uint8Array): Uint8Array[][] {
  const splits: Uint8Array[][] = [];
  for (let i = 0; i <= bytes.length; i += 1) {
    splits.push([bytes.subarray(0, i), bytes.subarray(i)]);
  }
  return splits;
}

function splitByteByByte(bytes: Uint8Array): Uint8Array[] {
  return Array.from(bytes, (byte) => new Uint8Array([byte]));
}

function assertNoDangerousSequences(escaped: string): void {
  const lower = escaped.toLowerCase();
  assert.equal(lower.includes("</script"), false, "contains </script");
  assert.equal(lower.includes("<script"), false, "contains <script");
  assert.equal(lower.includes("<!--"), false, "contains <!--");
  assert.equal(escaped.includes(LS), false, "contains raw U+2028");
  assert.equal(escaped.includes(PS), false, "contains raw U+2029");
}

const PAYLOADS: unknown[] = [
  "plain text",
  "</script>",
  "</SCRIPT >",
  "</ScRiPt\n>",
  "<script>alert(1)</script>",
  "<SCRIPT src=x>",
  "<!-- comment --><script>",
  "<!-<!---->",
  "<<script<<!--",
  "</scr" + "ipt>split source",
  "ends with angle <",
  "ends with partial </scr",
  "ends with partial <sc",
  "<s<sc<scr<scri<scrip<script",
  `line${LS}separator`,
  `paragraph${PS}separator`,
  `${LS}${PS}${LS}`,
  `emoji 😀🎉 and CJK 中文日本語`,
  `😀</script>🎉<!--中文`,
  `<${LS}script>not a tag`,
  `mixed 😀<script>${LS}</script>${PS}<!--`,
  "",
  "<",
  "<!",
  "<!-",
  "</",
  "</s",
  { nested: ["</script>", { deep: `<!--${LS}` }], n: 42, b: true, z: null },
  ["<script>", "</script>", LS, PS, "😀"],
  "long".repeat(500) + "</script>" + "x".repeat(500),
];

test("one-shot escaping neutralizes dangerous sequences", () => {
  assert.equal(escapeInlineScriptJson("</script>"), "\\u003C/script>");
  assert.equal(escapeInlineScriptJson("<script>"), "\\u003Cscript>");
  assert.equal(escapeInlineScriptJson("<!--"), "\\u003C!--");
  assert.equal(escapeInlineScriptJson("</SCRIPT"), "\\u003C/SCRIPT");
  assert.equal(escapeInlineScriptJson(`a${LS}b${PS}c`), "a\\u2028b\\u2029c");
  assert.equal(escapeInlineScriptJson("safe < text"), "safe < text");
  assert.equal(escapeInlineScriptJson("<div>"), "<div>");
  assert.equal(escapeInlineScriptJson("trailing <"), "trailing <");
  assert.equal(escapeInlineScriptJson("trailing </scr"), "trailing </scr");
});

for (const [index, payload] of PAYLOADS.entries()) {
  const json = JSON.stringify(payload);
  const oneShot = escapeInlineScriptJson(json);
  const bytes = toBytes(json);

  test(`payload ${index}: escaped output parses back to the original value`, () => {
    assert.deepEqual(JSON.parse(oneShot), payload);
    assertNoDangerousSequences(oneShot);
  });

  test(`payload ${index}: every two-chunk byte split matches one-shot`, () => {
    for (const chunks of splitAtEveryByte(bytes)) {
      assert.equal(runStreaming(chunks), oneShot);
    }
  });

  test(`payload ${index}: byte-by-byte streaming matches one-shot`, () => {
    assert.equal(runStreaming(splitByteByByte(bytes)), oneShot);
  });

  test(`payload ${index}: random multi-chunk splits match one-shot`, () => {
    let seed = index * 2654435761 + 1;
    const random = () => {
      seed = (seed * 1103515245 + 12345) & 0x7fffffff;
      return seed / 0x7fffffff;
    };
    for (let round = 0; round < 20; round += 1) {
      const chunks: Uint8Array[] = [];
      let offset = 0;
      while (offset < bytes.length) {
        const size = Math.min(
          bytes.length - offset,
          1 + Math.floor(random() * 17),
        );
        chunks.push(bytes.subarray(offset, offset + size));
        offset += size;
      }
      assert.equal(runStreaming(chunks), oneShot);
    }
  });
}

test("push after end throws", () => {
  const escaper = new StreamingScriptJsonEscaper();
  escaper.end();
  assert.throws(() => escaper.push("x"));
  assert.throws(() => escaper.end());
});

test("pending tail is not released before disambiguation", () => {
  const escaper = new StreamingScriptJsonEscaper();
  assert.equal(escaper.push(toBytes("<")), "");
  assert.equal(escaper.push(toBytes("/s")), "");
  assert.equal(escaper.push(toBytes("cri")), "");
  assert.equal(escaper.push(toBytes("pt>")), "\\u003C/script>");
  assert.equal(escaper.end(), "");
});

test("UTF-8 multi-byte sequence split across chunks", () => {
  const escaper = new StreamingScriptJsonEscaper();
  const bytes = toBytes("😀");
  assert.equal(escaper.push(bytes.subarray(0, 2)), "");
  assert.equal(escaper.push(bytes.subarray(2)), "😀");
  assert.equal(escaper.end(), "");
});

import { test } from "node:test";
import assert from "node:assert/strict";
import {
  TextDocument,
  build,
  getText,
  iterateRange,
  MAX_LEAF_SIZE,
  type TextNode,
} from "../src/rope.ts";

// ---------- helpers ----------

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

const PIECES = ["a", "bc", "hello", "\n", "\r\n", "😀", "🚀🎉", "é", "中文字符", "x".repeat(50)];

function randomPiece(rand: () => number): string {
  return PIECES[Math.floor(rand() * PIECES.length)];
}

function randomText(rand: () => number, pieces: number): string {
  let out = "";
  for (let i = 0; i < pieces; i++) out += randomPiece(rand);
  return out;
}

/** Valid edit boundaries: not mid-surrogate, not mid-CRLF. */
function validBoundaries(text: string): number[] {
  const out: number[] = [];
  for (let i = 0; i <= text.length; i++) {
    const before = i > 0 ? text.charCodeAt(i - 1) : -1;
    const after = i < text.length ? text.charCodeAt(i) : -1;
    const midSurrogate =
      before >= 0xd800 && before <= 0xdbff && after >= 0xdc00 && after <= 0xdfff;
    const midCrlf = before === 0x0d && after === 0x0a;
    if (!midSurrogate && !midCrlf) out.push(i);
  }
  return out;
}

function refPositionAt(text: string, offset: number): { line: number; character: number } {
  let line = 0;
  let lineStart = 0;
  for (let i = 0; i < offset; i++) {
    if (text[i] === "\n") {
      line++;
      lineStart = i + 1;
    }
  }
  return { line, character: offset - lineStart };
}

function collectNodes(root: TextNode, set = new Set<TextNode>()): Set<TextNode> {
  set.add(root);
  if (root.kind === "branch") {
    collectNodes(root.left, set);
    collectNodes(root.right, set);
  }
  return set;
}

function checkCaches(root: TextNode): void {
  if (root.kind === "leaf") {
    assert.equal(root.length, root.text.length);
    let lf = 0;
    for (const ch of root.text) if (ch === "\n") lf++;
    assert.equal(root.newlines, lf);
    return;
  }
  assert.equal(root.length, root.left.length + root.right.length);
  assert.equal(root.newlines, root.left.newlines + root.right.newlines);
  checkCaches(root.left);
  checkCaches(root.right);
}

// ---------- basics ----------

test("build/getText round-trip and cached length/newlines (LF + CRLF)", () => {
  const text = "alpha\nbeta\r\ngamma\r\ndelta\n😀 emoji";
  const doc = TextDocument.from(text);
  assert.equal(doc.getText(), text);
  assert.equal(doc.length, text.length);
  assert.equal(doc.lineCount, 5); // 4 line breaks: \n \r\n \r\n \n
  checkCaches(doc.root);
});

test("empty document", () => {
  const doc = TextDocument.empty();
  assert.equal(doc.length, 0);
  assert.equal(doc.lineCount, 1);
  assert.equal(doc.getText(), "");
  const doc2 = doc.insert(0, "hi\n");
  assert.equal(doc2.getText(), "hi\n");
  assert.equal(doc.getText(), ""); // original untouched
});

test("insert / delete / replace basic", () => {
  const v1 = TextDocument.from("hello world");
  const v2 = v1.insert(5, " brave new");
  assert.equal(v2.getText(), "hello brave new world");
  const v3 = v2.delete(5, 15);
  assert.equal(v3.getText(), "hello world");
  const v4 = v3.replace(6, 11, "rope");
  assert.equal(v4.getText(), "hello rope");
  assert.equal(v1.getText(), "hello world");
});

// ---------- CRLF / surrogate boundary rules ----------

test("insert in the middle of CRLF is rejected", () => {
  const doc = TextDocument.from("a\r\nb");
  // offsets: a=0 \r=1 \n=2 b=3 ; offset 2 is between \r and \n
  assert.throws(() => doc.insert(2, "X"), RangeError);
  assert.throws(() => doc.insert(2, "X"), /inside a CRLF/);
  // boundaries around the pair are fine
  assert.equal(doc.insert(1, "X").getText(), "aX\r\nb");
  assert.equal(doc.insert(3, "X").getText(), "a\r\nXb");
});

test("delete/replace boundaries may not split CRLF", () => {
  const doc = TextDocument.from("a\r\nb");
  assert.throws(() => doc.delete(1, 2), RangeError); // would remove lone \r
  assert.throws(() => doc.delete(2, 3), RangeError); // would remove lone \n
  assert.throws(() => doc.replace(1, 2, "x"), RangeError);
  assert.equal(doc.delete(1, 3).getText(), "ab"); // removing whole CRLF is fine
});

test("insert inside a surrogate pair is rejected", () => {
  const doc = TextDocument.from("x😀y"); // x=0, high=1, low=2, y=3
  assert.throws(() => doc.insert(2, "Z"), RangeError);
  assert.throws(() => doc.insert(2, "Z"), /surrogate pair/);
  assert.equal(doc.insert(1, "Z").getText(), "xZ😀y");
  assert.equal(doc.insert(3, "Z").getText(), "x😀Zy");
  assert.throws(() => doc.delete(1, 2), RangeError); // lone high surrogate
});

test("out-of-range edits are rejected", () => {
  const doc = TextDocument.from("abc");
  assert.throws(() => doc.insert(4, "x"), RangeError);
  assert.throws(() => doc.insert(-1, "x"), RangeError);
  assert.throws(() => doc.delete(2, 1), RangeError);
  assert.throws(() => doc.delete(0, 4), RangeError);
});

// ---------- line/column positioning ----------

test("line/column positioning with LF and CRLF", () => {
  const text = "one\ntwo\r\nthree\r\n😀four";
  const doc = TextDocument.from(text);
  assert.equal(doc.lineCount, 4);
  // offset of 't' in "two"
  const twoStart = text.indexOf("two");
  assert.deepEqual(doc.positionAt(twoStart), { line: 1, character: 0 });
  assert.equal(doc.offsetAt({ line: 1, character: 0 }), twoStart);
  // offset of 't' in "three"
  const threeStart = text.indexOf("three");
  assert.deepEqual(doc.positionAt(threeStart), { line: 2, character: 0 });
  // emoji line: "😀four" starts at line 3
  const emojiStart = text.indexOf("😀");
  assert.deepEqual(doc.positionAt(emojiStart), { line: 3, character: 0 });
  assert.deepEqual(doc.positionAt(emojiStart + 2), { line: 3, character: 2 });
  assert.equal(doc.offsetAt({ line: 3, character: 2 }), emojiStart + 2);
});

test("positioning around emoji is reversible for every offset", () => {
  const text = "a😀b\r\n🚀🎉c\né😀";
  const doc = TextDocument.from(text);
  for (let offset = 0; offset <= text.length; offset++) {
    const pos = doc.positionAt(offset);
    assert.equal(doc.offsetAt(pos), offset, `round-trip failed at offset ${offset}`);
    assert.deepEqual(pos, refPositionAt(text, offset));
  }
});

test("positionAt/offsetAt reject invalid input", () => {
  const doc = TextDocument.from("a\nb");
  assert.throws(() => doc.positionAt(4), RangeError);
  assert.throws(() => doc.offsetAt({ line: 2, character: 0 }), RangeError);
  assert.throws(() => doc.offsetAt({ line: 0, character: 2 }), RangeError);
  assert.equal(doc.offsetAt({ line: 0, character: 1 }), 1); // end of line 0
});

// ---------- range iteration ----------

test("range iteration yields chunks without building full text", () => {
  const rand = mulberry32(7);
  const text = randomText(rand, 4000); // ~40KB, many leaves
  const doc = TextDocument.from(text);
  assert.ok(doc.length > MAX_LEAF_SIZE * 4);

  const start = 1234;
  const end = 5678;
  const chunks = [...doc.chunks(start, end)];
  assert.equal(chunks.join(""), text.slice(start, end));
  // proves no whole-document concatenation happened: yielded total == slice size
  assert.equal(chunks.reduce((n, c) => n + c.length, 0), end - start);

  // full iteration also matches
  assert.equal([...doc.chunks()].join(""), text);
  // empty range yields nothing
  assert.equal([...doc.chunks(10, 10)].length, 0);
  assert.throws(() => doc.chunks(-1, 5), RangeError);
});

// ---------- persistence / structural sharing ----------

test("three versions share unchanged nodes and never pollute each other", () => {
  const rand = mulberry32(42);
  const base = randomText(rand, 3000);
  const v1 = TextDocument.from(base);

  const insertAt = Math.floor(base.length / 2);
  const v2 = v1.insert(insertAt, "INSERTED-😀-TEXT\r\n");
  const v3 = v2.delete(100, 200);

  // grandparent / parent / child reads are independent
  assert.equal(v1.getText(), base);
  assert.equal(v2.getText(), base.slice(0, insertAt) + "INSERTED-😀-TEXT\r\n" + base.slice(insertAt));
  assert.equal(v3.getText(), v2.getText().slice(0, 100) + v2.getText().slice(200));
  // repeated reads stay stable
  assert.equal(v1.getText(), base);
  assert.equal(v1.length, base.length);

  // structural sharing: v2 and v3 reuse most of v1's nodes
  const n1 = collectNodes(v1.root);
  const n2 = collectNodes(v2.root);
  const n3 = collectNodes(v3.root);
  const shared12 = [...n2].filter((n) => n1.has(n)).length;
  const shared13 = [...n3].filter((n) => n1.has(n)).length;
  assert.ok(shared12 > n1.size * 0.8, `v1/v2 share ${shared12}/${n1.size} nodes`);
  assert.ok(shared13 > n1.size * 0.7, `v1/v3 share ${shared13}/${n1.size} nodes`);
  checkCaches(v1.root);
  checkCaches(v2.root);
  checkCaches(v3.root);
});

test("small edit on a large document copies only O(log n) nodes", () => {
  const rand = mulberry32(99);
  const big = randomText(rand, 20000); // ~200KB
  const v1 = TextDocument.from(big);
  const v2 = v1.insert(Math.floor(v1.length / 2), "tiny");
  const before = collectNodes(v1.root);
  const fresh = [...collectNodes(v2.root)].filter((n) => !before.has(n)).length;
  const total = before.size;
  assert.ok(fresh < total * 0.2, `fresh nodes ${fresh} of ${total}`);
  assert.equal(v1.getText(), big);
});

// ---------- randomized differential test against plain strings ----------

test("randomized ops match string reference (LF/CRLF/emoji)", () => {
  for (const seed of [1, 2, 3]) {
    const rand = mulberry32(seed);
    let ref = randomText(rand, 200);
    let doc = TextDocument.from(ref);

    for (let step = 0; step < 400; step++) {
      const op = Math.floor(rand() * 3);
      const bounds = validBoundaries(ref);
      const a = bounds[Math.floor(rand() * bounds.length)];
      const b = bounds[Math.floor(rand() * bounds.length)];
      const start = Math.min(a, b);
      const end = Math.max(a, b);

      if (op === 0) {
        const ins = randomText(rand, 3);
        doc = doc.insert(start, ins);
        ref = ref.slice(0, start) + ins + ref.slice(start);
      } else if (op === 1) {
        doc = doc.delete(start, end);
        ref = ref.slice(0, start) + ref.slice(end);
      } else {
        const ins = randomText(rand, 2);
        doc = doc.replace(start, end, ins);
        ref = ref.slice(0, start) + ins + ref.slice(end);
      }

      assert.equal(doc.getText(), ref, `seed=${seed} step=${step} op=${op}`);
      assert.equal(doc.length, ref.length);
      checkCaches(doc.root);

      if (step % 25 === 0 && ref.length > 0) {
        const offset = Math.floor(rand() * (ref.length + 1));
        assert.deepEqual(doc.positionAt(offset), refPositionAt(ref, offset));
        assert.equal(doc.offsetAt(doc.positionAt(offset)), offset);
        const s = Math.floor(rand() * (ref.length + 1));
        const e = Math.floor(rand() * (ref.length + 1));
        const [lo, hi] = s <= e ? [s, e] : [e, s];
        assert.equal([...doc.chunks(lo, hi)].join(""), ref.slice(lo, hi));
      }
    }
  }
});

test("line counts with mixed LF / CRLF / lone CR", () => {
  const doc = TextDocument.from("a\nb\r\nc\rd\n\n");
  // \n after a, \r\n after b, lone \r (not a break), \n after d, trailing \n
  assert.equal(doc.lineCount, 5);
  assert.deepEqual(doc.positionAt(doc.length), { line: 4, character: 0 });
  assert.equal(getText(build("x".repeat(MAX_LEAF_SIZE * 3 + 7))).length, MAX_LEAF_SIZE * 3 + 7);
});

test("iterateRange across many leaves matches slice", () => {
  const rand = mulberry32(1234);
  const text = randomText(rand, 6000);
  const root = build(text);
  for (let i = 0; i < 50; i++) {
    const s = Math.floor(rand() * (text.length + 1));
    const e = Math.floor(rand() * (text.length + 1));
    const [lo, hi] = s <= e ? [s, e] : [e, s];
    assert.equal([...iterateRange(root, lo, hi)].join(""), text.slice(lo, hi));
  }
});

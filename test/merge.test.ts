import { test } from "node:test";
import assert from "node:assert/strict";
import { merge3, detectEol, isUtf16Boundary } from "../src/merge.ts";

const LF = "\n";
const CRLF = "\r\n";

/** 断言输出中包含了两侧新增的全部内容行（不静默丢内容）。 */
function assertContainsAll(text: string, chunks: string[]): void {
  for (const chunk of chunks) {
    assert.ok(
      text.includes(chunk),
      `merged text should contain ${JSON.stringify(chunk)}`,
    );
  }
}

test("互不重叠修改自动合并：一侧改首行，另一侧改末行（LF）", () => {
  const base = ["alpha", "beta", "gamma"].join(LF) + LF;
  const ours = ["ALPHA", "beta", "gamma"].join(LF) + LF;
  const theirs = ["alpha", "beta", "GAMMA"].join(LF) + LF;
  const result = merge3(base, ours, theirs);
  assert.equal(result.clean, true);
  assert.equal(result.conflicts.length, 0);
  assert.equal(result.text, ["ALPHA", "beta", "GAMMA"].join(LF) + LF);
});

test("互不重叠修改自动合并：一侧改首行，另一侧改末行（CRLF）", () => {
  const base = ["alpha", "beta", "gamma"].join(CRLF) + CRLF;
  const ours = ["ALPHA", "beta", "gamma"].join(CRLF) + CRLF;
  const theirs = ["alpha", "beta", "GAMMA"].join(CRLF) + CRLF;
  const result = merge3(base, ours, theirs);
  assert.equal(result.clean, true);
  assert.equal(result.text, ["ALPHA", "beta", "GAMMA"].join(CRLF) + CRLF);
  assert.equal(result.eol, CRLF);
});

test("CRLF 风格在自动合并与冲突标记中都得到保留", () => {
  const base = ["a", "b", "c"].join(CRLF) + CRLF;
  const ours = ["a-ours", "b", "c"].join(CRLF) + CRLF;
  const theirs = ["a-theirs", "b", "c"].join(CRLF) + CRLF;
  const result = merge3(base, ours, theirs);
  assert.equal(result.clean, false);
  // 输出中不应出现裸 LF（所有 \n 前都是 \r）
  assert.equal(result.text.replace(/\r\n/g, "").includes("\n"), false);
  assert.ok(result.text.includes(`<<<<<<< ours${CRLF}`));
  assert.ok(result.text.includes(`=======${CRLF}`));
  assert.ok(result.text.includes(`>>>>>>> theirs${CRLF}`));
});

test("LF 文件不会被引入 CRLF", () => {
  const base = "a\nb\n";
  const result = merge3(base, "A\nb\n", "a\nB\n");
  assert.equal(result.text.includes("\r"), false);
  assert.equal(result.text, "A\nB\n");
});

test("重叠修改生成结构化冲突，且两侧内容都保留", () => {
  const base = "one\ntwo\nthree\n";
  const ours = "one\nTWO-ours\nthree\n";
  const theirs = "one\nTWO-theirs\nthree\n";
  const result = merge3(base, ours, theirs);
  assert.equal(result.clean, false);
  assert.equal(result.conflicts.length, 1);
  const conflict = result.conflicts[0];
  assert.deepEqual(conflict.base, ["two"]);
  assert.deepEqual(conflict.ours, ["TWO-ours"]);
  assert.deepEqual(conflict.theirs, ["TWO-theirs"]);
  assertContainsAll(result.text, ["TWO-ours", "TWO-theirs", "one\n", "three\n"]);
  const expected =
    "one\n" +
    "<<<<<<< ours\nTWO-ours\n=======\nTWO-theirs\n>>>>>>> theirs\n" +
    "three\n";
  assert.equal(result.text, expected);
});

test("双方同位置插入不同内容 → 按规则冲突", () => {
  const base = "head\ntail\n";
  const ours = "head\ninserted-by-ours\ntail\n";
  const theirs = "head\ninserted-by-theirs\ntail\n";
  const result = merge3(base, ours, theirs);
  assert.equal(result.clean, false);
  assert.equal(result.conflicts.length, 1);
  assert.deepEqual(result.conflicts[0].base, []);
  assert.deepEqual(result.conflicts[0].ours, ["inserted-by-ours"]);
  assert.deepEqual(result.conflicts[0].theirs, ["inserted-by-theirs"]);
  assertContainsAll(result.text, ["inserted-by-ours", "inserted-by-theirs"]);
});

test("相同修改只出现一次，且不算冲突", () => {
  const base = "x\ny\nz\n";
  const ours = "x\nY\nz\n";
  const theirs = "x\nY\nz\n";
  const result = merge3(base, ours, theirs);
  assert.equal(result.clean, true);
  assert.equal(result.text, "x\nY\nz\n");
  assert.equal(result.text.match(/Y/g)!.length, 1);
});

test("两侧在相同位置插入相同内容 → 只保留一份", () => {
  const base = "a\nb\n";
  const ours = "a\nnew\nb\n";
  const theirs = "a\nnew\nb\n";
  const result = merge3(base, ours, theirs);
  assert.equal(result.clean, true);
  assert.equal(result.text, "a\nnew\nb\n");
});

test("一侧删除、另一侧修改不相交区域 → 自动合并", () => {
  const base = "1\n2\n3\n4\n";
  const ours = "1\n3\n4\n"; // 删除第 2 行
  const theirs = "1\n2\n3\nFOUR\n"; // 修改第 4 行
  const result = merge3(base, ours, theirs);
  assert.equal(result.clean, true);
  assert.equal(result.text, "1\n3\nFOUR\n");
});

test("只有一侧有修改时直接采用该侧", () => {
  const base = "a\nb\n";
  const result = merge3(base, "a\nb\nc\n", base);
  assert.equal(result.clean, true);
  assert.equal(result.text, "a\nb\nc\n");
});

test("空 base：双方都新增不同内容 → 冲突且内容都在", () => {
  const result = merge3("", "from-ours\n", "from-theirs\n");
  assert.equal(result.clean, false);
  assert.equal(result.conflicts.length, 1);
  assertContainsAll(result.text, ["from-ours", "from-theirs"]);
});

test("空 base：仅一侧新增 → 干净合并", () => {
  const result = merge3("", "hello\n", "");
  assert.equal(result.clean, true);
  assert.equal(result.text, "hello\n");
});

test("末尾换行差异不丢失：一侧补末尾换行，另一侧改首行", () => {
  const base = "first\nlast"; // 无末尾换行
  const ours = "FIRST\nlast";
  const theirs = "first\nlast\n"; // 补上末尾换行
  const result = merge3(base, ours, theirs);
  assert.equal(result.clean, true);
  assert.equal(result.text, "FIRST\nlast\n");
});

test("UTF-16 偏移：含 emoji（代理对）时冲突偏移精确且不切开代理对", () => {
  const base = "intro 😀🎉 line\nmiddle\nend\n";
  const ours = "intro 😀🎉 line\nOURS\nend\n";
  const theirs = "intro 😀🎉 line\nTHEIRS\nend\n";
  const result = merge3(base, ours, theirs);
  assert.equal(result.conflicts.length, 1);
  const conflict = result.conflicts[0];

  // 偏移是 UTF-16 码元偏移：emoji 各占 2 个码元
  const expectedStart = "intro 😀🎉 line\n".length;
  assert.equal(conflict.startOffset, expectedStart);
  assert.equal(
    result.text.slice(conflict.startOffset, conflict.endOffset),
    "<<<<<<< ours\nOURS\n=======\nTHEIRS\n>>>>>>> theirs\n",
  );
  assert.equal(result.text.slice(conflict.endOffset), "end\n");

  // 偏移不得切开代理对
  assert.ok(isUtf16Boundary(result.text, conflict.startOffset));
  assert.ok(isUtf16Boundary(result.text, conflict.endOffset));
});

test("UTF-16 偏移：CRLF 下同样精确", () => {
  const base = "α β 😀\r\nmid\r\nlast\r\n";
  const ours = "α β 😀\r\nO\r\nlast\r\n";
  const theirs = "α β 😀\r\nT\r\nlast\r\n";
  const result = merge3(base, ours, theirs);
  const conflict = result.conflicts[0];
  assert.equal(conflict.startOffset, "α β 😀\r\n".length);
  assert.equal(
    result.text.slice(conflict.startOffset, conflict.endOffset),
    "<<<<<<< ours\r\nO\r\n=======\r\nT\r\n>>>>>>> theirs\r\n",
  );
  assert.ok(isUtf16Boundary(result.text, conflict.startOffset));
  assert.ok(isUtf16Boundary(result.text, conflict.endOffset));
});

test("isUtf16Boundary 能识别代理对中间的位置", () => {
  const text = "a😀b"; // 😀 = 􀀀 两个码元
  assert.equal(isUtf16Boundary(text, 0), true);
  assert.equal(isUtf16Boundary(text, 1), true);
  assert.equal(isUtf16Boundary(text, 2), false); // 代理对中间
  assert.equal(isUtf16Boundary(text, 3), true);
  assert.equal(isUtf16Boundary(text, 4), true);
});

test("detectEol：CRLF 占优判 CRLF，否则 LF", () => {
  assert.equal(detectEol("a\r\nb\r\n"), "\r\n");
  assert.equal(detectEol("a\nb\n"), "\n");
  assert.equal(detectEol(""), "\n");
  assert.equal(detectEol("a\r\nb\nc\r\n"), "\r\n");
});

test("options.eol 可强制输出换行风格", () => {
  const base = "a\nb\n";
  const result = merge3(base, "A\nb\n", "a\nB\n", { eol: "crlf" });
  assert.equal(result.text, "A\r\nB\r\n");
  assert.equal(result.eol, "\r\n");
});

test("自定义冲突标签", () => {
  const result = merge3("v\n", "v1\n", "v2\n", {
    oursLabel: "HEAD",
    theirsLabel: "feature",
  });
  assert.ok(result.text.includes("<<<<<<< HEAD\n"));
  assert.ok(result.text.includes(">>>>>>> feature\n"));
});

test("多区域：两个冲突 + 一个自动合并区域", () => {
  const base = "1\n2\n3\n4\n5\n";
  const ours = "ONE\n2\n3\n4\nFIVE\n";
  const theirs = "one\n2\n3\n4\nfive\n";
  const result = merge3(base, ours, theirs);
  assert.equal(result.conflicts.length, 2);
  assertContainsAll(result.text, ["ONE", "one", "FIVE", "five"]);
  // 中间未触碰的行保持原样且只出现一次
  assert.equal(result.text.match(/2\n/g)!.length, 1);
});

test("一侧在文件开头插入、另一侧在文件末尾插入 → 自动合并", () => {
  const base = "mid\n";
  const ours = "top\nmid\n";
  const theirs = "mid\nbottom\n";
  const result = merge3(base, ours, theirs);
  assert.equal(result.clean, true);
  assert.equal(result.text, "top\nmid\nbottom\n");
});

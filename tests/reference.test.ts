import { test, assert, mulberry32 } from "./harness.ts";
import { visualColumn } from "../src/indent.ts";
import { parse, reprint } from "../src/parser.ts";
import { referenceAnalyze } from "../src/reference.ts";

/** Structural view of the main parser, comparable to the reference model. */
function parserAnalyze(src: string) {
  const { file, diagnostics } = parse(src);
  const logicalLines = file.lines.filter((l) => l.kind === "entry" && l.depth >= 0);
  const physToLogical = new Map(logicalLines.map((l, i) => [l.index, i]));
  return {
    logical: logicalLines.map((l) => ({ phys: l.index + 1, col: visualColumn(l.indent) })),
    parents: logicalLines.map((l) => (l.parent >= 0 ? physToLogical.get(l.parent)! : -1)),
    errors: diagnostics.map((d) => ({ line: d.line, code: d.code as string })),
  };
}

function assertSameModel(src: string): void {
  assert.deepEqual(parserAnalyze(src), referenceAnalyze(src));
}

test("reference model agrees on targeted recovery scenarios", () => {
  const cases = [
    "a:\n    x: 1\n  bad: 2\n    skipped: 9\nc: 3\n",
    "a:\n        x: 1\nb:\n\tconflict: 2\n\tstill: skipped\nok: 1\n",
    "broken\na: 1\n  also-broken: 2\nb: 2\n",
    "a:\n  x:\n    deep: 1\n  y: 2\nz: 3\n",
    "a: 1\n    b: 2\nc:\n\td: 3\n",
    "no colon here\n: empty key\nfine: 1\n",
  ];
  for (const src of cases) assertSameModel(src);
});

const INDENTS = ["", " ", "  ", "   ", "    ", "\t", "\t\t", " \t", "  \t", "\t ", "        ", " \t "];
const KEYS = ["a", "b", "cfg", "key 1", "x"];
const VALS = ["1", "hello", "a b", "true", ""];

function genDoc(rand: () => number): string {
  const n = Math.floor(rand() * 14);
  const parts: string[] = [];
  for (let i = 0; i < n; i++) {
    const indent = INDENTS[Math.floor(rand() * INDENTS.length)]!;
    const r = rand();
    let body: string;
    if (r < 0.12) body = indent + (rand() < 0.5 ? "" : "   ");
    else if (r < 0.27) body = indent + "# note " + i;
    else if (r < 0.37) body = indent + "broken" + i;
    else if (r < 0.42) body = indent + ": novalue";
    else {
      const key = KEYS[Math.floor(rand() * KEYS.length)]!;
      const val = VALS[Math.floor(rand() * VALS.length)]!;
      body = indent + key + ":" + (val === "" ? "" : " " + val);
    }
    parts.push(body + (rand() < 0.1 ? "\r\n" : "\n"));
  }
  let src = parts.join("");
  if (rand() < 0.3 && src.endsWith("\n")) {
    src = src.slice(0, src.endsWith("\r\n") ? -2 : -1);
  }
  return src;
}

test("differential fuzz: parser matches explicit-stack reference model", () => {
  for (let seed = 1; seed <= 3000; seed++) {
    const src = genDoc(mulberry32(seed));
    let actual: ReturnType<typeof parserAnalyze>;
    let expected: ReturnType<typeof referenceAnalyze>;
    try {
      actual = parserAnalyze(src);
      expected = referenceAnalyze(src);
    } catch (err) {
      throw new Error(`seed ${seed} threw for ${JSON.stringify(src)}: ${err}`);
    }
    try {
      assert.deepEqual(actual, expected);
    } catch (err) {
      throw new Error(
        `seed ${seed} mismatch for ${JSON.stringify(src)}\n` +
          `parser:    ${JSON.stringify(actual)}\n` +
          `reference: ${JSON.stringify(expected)}`,
      );
    }
    // Full fidelity even for documents with errors.
    assert.equal(reprint(parse(src).file), src, `seed ${seed} reprint`);
  }
});

import { test, assert } from "./harness.ts";
import { visualColumn } from "../src/indent.ts";
import { parse, reprint } from "../src/parser.ts";
import { semantics } from "../src/semantics.ts";

test("visualColumn: tabs advance to 8-column tab stops", () => {
  assert.equal(visualColumn(""), 0);
  assert.equal(visualColumn("    "), 4);
  assert.equal(visualColumn("\t"), 8);
  assert.equal(visualColumn(" \t"), 8);
  assert.equal(visualColumn("  \t"), 8);
  assert.equal(visualColumn("        "), 8);
  assert.equal(visualColumn("\t \t"), 16);
  assert.equal(visualColumn("\t\t"), 16);
});

test("basic nesting, values and full-fidelity reprint", () => {
  const src = "# c\n\nserver:\n    host: example.com\n    port: 8080\n";
  const { file, diagnostics } = parse(src);
  assert.deepEqual(diagnostics, []);
  assert.equal(reprint(file), src);
  assert.deepEqual(semantics(file), [
    {
      key: "server",
      value: null,
      children: [
        { key: "host", value: "example.com", children: [] },
        { key: "port", value: "8080", children: [] },
      ],
    },
  ]);
});

test("comments and blank lines are skipped but preserved", () => {
  const src = "a:\n  # note\n\n  x: 1\n# tail\n";
  const { file, diagnostics } = parse(src);
  assert.deepEqual(diagnostics, []);
  assert.equal(reprint(file), src);
  assert.deepEqual(semantics(file), [
    { key: "a", value: null, children: [{ key: "x", value: "1", children: [] }] },
  ]);
});

test("tab indents nest at column 8", () => {
  const src = "a:\n\tb:\n\t\tc: 2\n";
  const { file, diagnostics } = parse(src);
  assert.deepEqual(diagnostics, []);
  assert.equal(file.lines[1]!.depth, 1);
  assert.equal(file.lines[2]!.depth, 2);
  assert.equal(reprint(file), src);
});

test("mixed prefixes aligning on a tab stop share a level", () => {
  // "  \t" and 8 spaces both reach column 8; same prefix reused is fine,
  // but two *different* prefixes at one column are ambiguous.
  const ok = parse("a:\n  \tx: 1\n  \ty: 2\n");
  assert.deepEqual(ok.diagnostics, []);
  const bad = parse("a:\n        x: 1\nb:\n\ty: 2\n");
  assert.equal(bad.diagnostics.length, 1);
  const d = bad.diagnostics[0]!;
  assert.equal(d.code, "ambiguous-indent");
  assert.equal(d.line, 4);
  assert.equal(d.col, 1);
  assert.equal(d.endCol, 2);
  assert.match(d.message, /column 8/);
  assert.equal(reprint(bad.file), bad.file.src);
});

test("dedent must match an enclosing level", () => {
  const src = "a:\n    x: 1\n  y: 2\n";
  const { diagnostics, file } = parse(src);
  assert.equal(diagnostics.length, 1);
  assert.equal(diagnostics[0]!.code, "bad-dedent");
  assert.equal(diagnostics[0]!.line, 3);
  assert.equal(diagnostics[0]!.col, 1);
  assert.equal(diagnostics[0]!.endCol, 3);
  assert.equal(reprint(file), src);
});

test("indent after a leaf value is unexpected", () => {
  const src = "a: 1\n    b: 2\n";
  const { diagnostics } = parse(src);
  assert.equal(diagnostics.length, 1);
  assert.equal(diagnostics[0]!.code, "unexpected-indent");
  assert.equal(diagnostics[0]!.line, 2);
});

test("first line may not be indented", () => {
  const { diagnostics } = parse("    a: 1\n");
  assert.equal(diagnostics.length, 1);
  assert.equal(diagnostics[0]!.code, "unexpected-indent");
});

test("missing colon and empty key are syntax errors", () => {
  const a = parse("just text\n");
  assert.equal(a.diagnostics[0]!.code, "missing-colon");
  assert.equal(a.diagnostics[0]!.line, 1);
  const b = parse(": 1\n");
  assert.equal(b.diagnostics[0]!.code, "empty-key");
});

test("error recovery resumes at the next top-level line", () => {
  const src = "a:\n    x: 1\n  bad: 2\n    skipped: 9\nc: 3\n";
  const { file, diagnostics } = parse(src);
  assert.equal(diagnostics.length, 1);
  assert.equal(diagnostics[0]!.code, "bad-dedent");
  assert.equal(diagnostics[0]!.line, 3);
  // The indented line after the error is skipped; the top-level line parses.
  assert.deepEqual(semantics(file), [
    { key: "a", value: null, children: [{ key: "x", value: "1", children: [] }] },
    { key: "c", value: "3", children: [] },
  ]);
  assert.equal(reprint(file), src);
});

test("recovery also works after ambiguity and syntax errors", () => {
  const src = "a:\n        x: 1\nb:\n\tconflict: 2\n\tstill: skipped\nok: 1\nbroken line\nafter: 2\n";
  const { file, diagnostics } = parse(src);
  assert.deepEqual(
    diagnostics.map((d) => [d.code, d.line]),
    [
      ["ambiguous-indent", 4],
      ["missing-colon", 7],
    ],
  );
  assert.deepEqual(
    semantics(file).map((n) => n.key),
    ["a", "b", "ok", "after"],
  );
  assert.equal(reprint(file), src);
});

test("CRLF line endings are preserved byte-for-byte", () => {
  const src = "a:\r\n    x: 1\r\n# c\r\n";
  const { file, diagnostics } = parse(src);
  assert.deepEqual(diagnostics, []);
  assert.equal(reprint(file), src);
  assert.equal(file.lines[0]!.newline, "\r\n");
});

test("empty and whitespace-only documents", () => {
  assert.deepEqual(parse("").diagnostics, []);
  assert.deepEqual(semantics(parse("").file), []);
  const ws = parse("   \n\t\n# only\n");
  assert.deepEqual(ws.diagnostics, []);
  assert.equal(reprint(ws.file), "   \n\t\n# only\n");
});

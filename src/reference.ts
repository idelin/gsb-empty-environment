/**
 * Independent reference model for the indentation grammar, written as a
 * single explicit-stack pass and intentionally not sharing logic with the
 * main parser. Differential tests compare hierarchy, diagnostics and
 * error recovery (skip to the next top-level line) between the two.
 */

export interface RefLogicalLine {
  /** 1-based physical line. */
  phys: number;
  col: number;
}

export interface RefError {
  /** 1-based physical line. */
  line: number;
  code: string;
}

export interface RefAnalysis {
  logical: RefLogicalLine[];
  /** Parent logical-line index per logical line, -1 for top level. */
  parents: number[];
  errors: RefError[];
}

export function referenceAnalyze(src: string): RefAnalysis {
  const raws = src === "" ? [] : src.split(/\r\n|\n|\r/);
  const levels: { col: number; prefix: string }[] = [{ col: 0, prefix: "" }];
  const seen = new Map<number, string>([[0, ""]]);
  const entries: number[] = [];
  const logical: RefLogicalLine[] = [];
  const parents: number[] = [];
  const errors: RefError[] = [];
  let prevOpener = false;
  let recovering = false;

  for (let i = 0; i < raws.length; i++) {
    const body = raws[i]!;
    const phys = i + 1;
    const indent = (body.match(/^[ \t]*/) as RegExpMatchArray)[0];
    const rest = body.slice(indent.length);
    if (rest === "" || rest.startsWith("#")) continue;
    let col = 0;
    for (const ch of indent) col = ch === "\t" ? col + 8 - (col % 8) : col + 1;
    if (recovering) {
      if (col !== 0) continue;
      recovering = false;
    }
    const ci = rest.indexOf(":");
    if (ci < 0) {
      errors.push({ line: phys, code: "missing-colon" });
      recovering = true;
      continue;
    }
    if (rest.slice(0, ci).trim() === "") {
      errors.push({ line: phys, code: "empty-key" });
      recovering = true;
      continue;
    }
    const known = seen.get(col);
    if (known !== undefined && known !== indent) {
      errors.push({ line: phys, code: "ambiguous-indent" });
      recovering = true;
      continue;
    }
    if (col > levels[levels.length - 1]!.col) {
      if (!prevOpener) {
        errors.push({ line: phys, code: "unexpected-indent" });
        recovering = true;
        continue;
      }
      levels.push({ col, prefix: indent });
      seen.set(col, indent);
    } else if (col < levels[levels.length - 1]!.col) {
      while (levels[levels.length - 1]!.col > col) levels.pop();
      if (levels[levels.length - 1]!.col !== col) {
        errors.push({ line: phys, code: "bad-dedent" });
        recovering = true;
        continue;
      }
    }
    const depth = levels.length - 1;
    parents.push(depth > 0 ? entries[depth - 1]! : -1);
    entries[depth] = logical.length;
    entries.length = depth + 1;
    logical.push({ phys, col });
    prevOpener = rest.slice(ci + 1).trim() === "";
  }
  return { logical, parents, errors };
}

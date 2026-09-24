/**
 * Three-way, line-based merge for inline text edits.
 *
 * Declared merge rules
 * --------------------
 * 1. Edits from the two sides that touch disjoint regions of the base
 *    document merge automatically.
 * 2. An insertion at offset p overlaps another edit iff the other edit is
 *    also an insertion at p, or its replaced range strictly contains p.
 *    Insertions at a range boundary (p === start or p === end) do NOT
 *    overlap and merge automatically.
 * 3. Overlapping edits that produce identical resulting text are applied
 *    once (identical changes are de-duplicated).
 * 4. Otherwise the overlap yields a structured conflict that carries the
 *    base, ours and theirs content; the merged text contains conflict
 *    markers so no side's content is ever silently dropped.
 * 5. The merged text uses the base document's dominant line-ending style
 *    (LF or CRLF); a missing trailing newline in the base is preserved.
 * 6. All public offsets are UTF-16 code-unit offsets (native JS string
 *    indices). Lines are only ever split at \n / \r\n, which are BMP
 *    characters, so surrogate pairs are never cut.
 */

export type Eol = '\n' | '\r\n';

/** A replacement of base lines [start, end) with `lines`. */
export interface LineEdit {
  start: number;
  end: number;
  lines: string[];
}

export interface Conflict {
  /** Conflicted region content, as arrays of lines. */
  base: string[];
  ours: string[];
  theirs: string[];
  /** UTF-16 offsets of the conflict block (incl. markers) in the merged output text. */
  start: number;
  end: number;
  /** UTF-16 offsets of the conflicted region in the base text. */
  baseStart: number;
  baseEnd: number;
}

export interface MergeResult {
  /** Merged text; contains conflict markers when `clean` is false. */
  text: string;
  /** True when no conflicts were produced. */
  clean: boolean;
  /** Structured conflicts, in document order. */
  conflicts: Conflict[];
  /** Line-ending style used by the output (inherited from the base). */
  eol: Eol;
}

function detectEol(text: string): Eol {
  const totalLf = (text.match(/\n/g) ?? []).length;
  const crlf = (text.match(/\r\n/g) ?? []).length;
  return crlf > 0 && crlf >= totalLf - crlf ? '\r\n' : '\n';
}

function splitLines(text: string): { lines: string[]; trailingNewline: boolean } {
  if (text === '') return { lines: [], trailingNewline: false };
  const lines = text.split(/\r\n|\n/);
  let trailingNewline = false;
  if (lines[lines.length - 1] === '') {
    lines.pop();
    trailingNewline = true;
  }
  return { lines, trailingNewline };
}

/** LCS-based line diff: edits that turn `base` into `next`. */
export function computeEdits(base: string[], next: string[]): LineEdit[] {
  const n = base.length;
  const m = next.length;
  const dp: Uint32Array[] = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i][j] = base[i] === next[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }
  const edits: LineEdit[] = [];
  let i = 0;
  let j = 0;
  let cur: LineEdit | null = null;
  const flush = () => {
    if (cur) edits.push(cur);
    cur = null;
  };
  while (i < n || j < m) {
    if (i < n && j < m && base[i] === next[j]) {
      flush();
      i++;
      j++;
    } else {
      if (!cur) cur = { start: i, end: i, lines: [] };
      if (j < m && (i === n || dp[i][j + 1] >= dp[i + 1][j])) {
        cur.lines.push(next[j]);
        j++;
      } else {
        i++;
        cur.end = i;
      }
    }
  }
  flush();
  return edits;
}

/** Overlap test implementing declared rule 2. */
function overlaps(x: LineEdit, y: LineEdit): boolean {
  const xEmpty = x.start === x.end;
  const yEmpty = y.start === y.end;
  if (xEmpty && yEmpty) return x.start === y.start;
  if (xEmpty) return y.start < x.start && x.start < y.end;
  if (yEmpty) return x.start < y.start && y.start < x.end;
  return x.start < y.end && y.start < x.end;
}

interface Cluster {
  start: number;
  end: number;
  a: LineEdit[];
  b: LineEdit[];
}

/** Group transitively overlapping edits from both sides into clusters. */
function clusterEdits(aEdits: LineEdit[], bEdits: LineEdit[]): Cluster[] {
  const clusters: Cluster[] = [];
  let i = 0;
  let j = 0;
  while (i < aEdits.length || j < bEdits.length) {
    const a = aEdits[i];
    const b = bEdits[j];
    let start: number;
    let end: number;
    const cluster: Cluster = { start: 0, end: 0, a: [], b: [] };
    if (b === undefined || (a !== undefined && (a.start < b.start || (a.start === b.start && a.end <= b.end)))) {
      start = a.start;
      end = a.end;
      cluster.a.push(a);
      i++;
    } else {
      start = b.start;
      end = b.end;
      cluster.b.push(b);
      j++;
    }
    let grew = true;
    while (grew) {
      grew = false;
      while (i < aEdits.length && overlaps(aEdits[i], { start, end, lines: [] })) {
        start = Math.min(start, aEdits[i].start);
        end = Math.max(end, aEdits[i].end);
        cluster.a.push(aEdits[i]);
        i++;
        grew = true;
      }
      while (j < bEdits.length && overlaps(bEdits[j], { start, end, lines: [] })) {
        start = Math.min(start, bEdits[j].start);
        end = Math.max(end, bEdits[j].end);
        cluster.b.push(bEdits[j]);
        j++;
        grew = true;
      }
    }
    cluster.start = start;
    cluster.end = end;
    clusters.push(cluster);
  }
  return clusters;
}

function applyEdits(baseSlice: string[], edits: LineEdit[], clusterStart: number): string[] {
  const out: string[] = [];
  let cursor = clusterStart;
  for (const e of edits) {
    for (let k = cursor; k < e.start; k++) out.push(baseSlice[k - clusterStart]);
    out.push(...e.lines);
    cursor = e.end;
  }
  for (let k = cursor; k < clusterStart + baseSlice.length; k++) out.push(baseSlice[k - clusterStart]);
  return out;
}

function arraysEqual(x: string[], y: string[]): boolean {
  return x.length === y.length && x.every((v, idx) => v === y[idx]);
}

/**
 * UTF-16 end offset of a region covering base lines [start, end):
 * points at the character right before the terminating newline (LF or
 * CRLF) of the last covered line, so slicing yields the region text
 * without a trailing line ending. Returns text.length when the region
 * ends at the very end of the document.
 */
function baseRegionEnd(
  text: string,
  lines: string[],
  offsets: number[],
  start: number,
  end: number,
): number {
  if (start === end) return offsets[start] ?? text.length;
  if (end >= lines.length) return text.length;
  const p = offsets[end];
  if (text.startsWith('\r\n', p - 2)) return p - 2;
  if (text[p - 1] === '\n') return p - 1;
  return p;
}

/** UTF-16 offset of each line start, scanning the real text (mixed EOL safe). */
function lineOffsets(text: string, lines: string[]): number[] {
  const offsets: number[] = [];
  let pos = 0;
  for (const line of lines) {
    offsets.push(pos);
    pos += line.length;
    if (text.startsWith('\r\n', pos)) pos += 2;
    else if (text[pos] === '\n') pos += 1;
  }
  return offsets;
}

/**
 * Merge `ours` and `theirs` against their common ancestor `base`.
 * Public offsets in the result are UTF-16 code-unit offsets.
 */
export function merge3(baseText: string, oursText: string, theirsText: string): MergeResult {
  const eol = detectEol(baseText);
  const base = splitLines(baseText);
  const ours = splitLines(oursText);
  const theirs = splitLines(theirsText);
  const clusters = clusterEdits(
    computeEdits(base.lines, ours.lines),
    computeEdits(base.lines, theirs.lines),
  );
  const baseLineOffsets = lineOffsets(baseText, base.lines);

  const parts: string[] = [];
  let outLen = 0;
  const pushLine = (line: string) => {
    parts.push(line, eol);
    outLen += line.length + eol.length;
  };

  const conflicts: Conflict[] = [];
  let cursor = 0;
  for (const cluster of clusters) {
    for (let k = cursor; k < cluster.start; k++) pushLine(base.lines[k]);
    const baseSlice = base.lines.slice(cluster.start, cluster.end);
    const oursLines = cluster.a.length > 0 ? applyEdits(baseSlice, cluster.a, cluster.start) : baseSlice;
    const theirsLines = cluster.b.length > 0 ? applyEdits(baseSlice, cluster.b, cluster.start) : baseSlice;
    if (cluster.a.length === 0) {
      theirsLines.forEach(pushLine);
    } else if (cluster.b.length === 0) {
      oursLines.forEach(pushLine);
    } else if (arraysEqual(oursLines, theirsLines)) {
      oursLines.forEach(pushLine); // identical change: apply once
    } else {
      const start = outLen;
      pushLine('<<<<<<< ours');
      oursLines.forEach(pushLine);
      pushLine('||||||| base');
      baseSlice.forEach(pushLine);
      pushLine('=======');
      theirsLines.forEach(pushLine);
      pushLine('>>>>>>> theirs');
      conflicts.push({
        base: baseSlice,
        ours: oursLines,
        theirs: theirsLines,
        start,
        end: outLen,
        baseStart: baseLineOffsets[cluster.start] ?? baseText.length,
        baseEnd: baseRegionEnd(baseText, base.lines, baseLineOffsets, cluster.start, cluster.end),
      });
    }
    cursor = cluster.end;
  }
  for (let k = cursor; k < base.lines.length; k++) pushLine(base.lines[k]);

  let text = parts.join('');
  if (!base.trailingNewline && text.endsWith(eol)) {
    text = text.slice(0, text.length - eol.length);
    for (const c of conflicts) c.end = Math.min(c.end, text.length);
  }
  return { text, clean: conflicts.length === 0, conflicts, eol };
}

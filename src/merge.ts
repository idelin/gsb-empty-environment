/**
 * 三路行级文本合并（three-way line merge）。
 *
 * 设计要点：
 * - 以共同祖先 base 为基准，分别对 ours / theirs 做行级 diff，再按 base 行区间归并。
 * - 互不重叠的修改自动合并；重叠（或同位置插入不同内容）的修改生成结构化冲突。
 * - 两侧完全相同的修改只保留一份。
 * - 输出保留 base 的换行风格（LF / CRLF），也可用 options.eol 强制指定。
 * - 对外暴露的偏移（MergeConflict.startOffset/endOffset）是 result.text 中的
 *   UTF-16 码元偏移（即 JS 字符串下标），且全部落在行边界上，绝不切开代理对。
 * - 冲突块中同时完整保留两侧内容，合并过程不会静默丢掉任何一侧的内容。
 */

export type EolStyle = "\n" | "\r\n";

export interface MergeOptions {
  /** 冲突块中 ours 侧的标签，默认 "ours"。 */
  oursLabel?: string;
  /** 冲突块中 theirs 侧的标签，默认 "theirs"。 */
  theirsLabel?: string;
  /** 换行风格："auto"（默认，按 base 内容检测）| "lf" | "crlf"。 */
  eol?: "auto" | "lf" | "crlf";
}

export interface MergeConflict {
  /** 冲突块（含 <<<<<<< 标记行）在 result.text 中的起始 UTF-16 偏移。 */
  startOffset: number;
  /** 冲突块（含 >>>>>>> 标记行）在 result.text 中的结束 UTF-16 偏移（不含）。 */
  endOffset: number;
  /** 冲突区域在 base 中的行（不含行尾换行符）。 */
  base: string[];
  /** ours 侧对该区域的替换内容。 */
  ours: string[];
  /** theirs 侧对该区域的替换内容。 */
  theirs: string[];
}

export interface MergeResult {
  /** 合并后的文本；冲突处以冲突标记块呈现，换行风格与 base 一致。 */
  text: string;
  /** 是否无冲突（conflicts.length === 0）。 */
  clean: boolean;
  /** 结构化冲突列表，按在 text 中出现的顺序排列。 */
  conflicts: MergeConflict[];
  /** 输出使用的换行风格。 */
  eol: EolStyle;
}

/** 一个差异块：把 base[baseStart, baseEnd) 替换为 lines。 */
interface Hunk {
  baseStart: number;
  baseEnd: number;
  lines: string[];
}

interface Region {
  start: number;
  end: number;
  oursHunks: Hunk[];
  theirsHunks: Hunk[];
}

/** 检测文本的主流换行风格。 */
export function detectEol(text: string): EolStyle {
  const total = (text.match(/\n/g) || []).length;
  const crlf = (text.match(/\r\n/g) || []).length;
  return crlf > total - crlf ? "\r\n" : "\n";
}

/**
 * 把文本切成“带行尾”的行：除可能的最后一行外，每行以 "\n" 结尾。
 * 内部统一把 CRLF 规范化为 LF，输出时再转换回目标风格。
 */
function splitLinesKeepEnds(text: string): string[] {
  const normalized = text.replace(/\r\n/g, "\n");
  if (normalized === "") return [];
  const parts = normalized.split("\n");
  const lines: string[] = [];
  for (let i = 0; i < parts.length - 1; i++) lines.push(parts[i] + "\n");
  const last = parts[parts.length - 1];
  if (last !== "") lines.push(last);
  return lines;
}

/** 把内部行序列拼接为目标换行风格的文本。 */
function joinLines(lines: string[], eol: EolStyle): string {
  if (eol === "\n") return lines.join("");
  let out = "";
  for (const line of lines) {
    out += line.endsWith("\n") ? line.slice(0, -1) + "\r\n" : line;
  }
  return out;
}

/** 去掉行尾 "\n"，用于结构化冲突里的内容展示。 */
function stripEnd(line: string): string {
  return line.endsWith("\n") ? line.slice(0, -1) : line;
}

function linesEqual(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}

/**
 * 基于 LCS 的行级 diff，输出把 a 变成 b 的 hunk 列表。
 * 先裁剪公共前后缀，再对中间部分做动态规划。
 */
function diffHunks(a: string[], b: string[]): Hunk[] {
  let start = 0;
  while (start < a.length && start < b.length && a[start] === b[start]) start++;
  let aEnd = a.length;
  let bEnd = b.length;
  while (aEnd > start && bEnd > start && a[aEnd - 1] === b[bEnd - 1]) {
    aEnd--;
    bEnd--;
  }
  const aMid = a.slice(start, aEnd);
  const bMid = b.slice(start, bEnd);
  const n = aMid.length;
  const m = bMid.length;

  const dp: number[][] = Array.from({ length: n + 1 }, () =>
    new Array<number>(m + 1).fill(0),
  );
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i][j] =
        aMid[i] === bMid[j]
          ? dp[i + 1][j + 1] + 1
          : Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }

  const hunks: Hunk[] = [];
  let current: Hunk | null = null;
  const ensure = (at: number): Hunk => {
    if (!current) current = { baseStart: at, baseEnd: at, lines: [] };
    return current;
  };
  const flush = (): void => {
    if (current) {
      hunks.push(current);
      current = null;
    }
  };

  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (aMid[i] === bMid[j]) {
      flush();
      i++;
      j++;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      ensure(start + i).baseEnd = start + i + 1;
      i++;
    } else {
      ensure(start + i).lines.push(bMid[j]);
      j++;
    }
  }
  while (i < n) {
    ensure(start + i).baseEnd = start + i + 1;
    i++;
  }
  while (j < m) {
    ensure(start + i).lines.push(bMid[j]);
    j++;
  }
  flush();
  return hunks;
}

/**
 * 归并两侧的 hunk：把 base 区间相交、或同为同一位置插入的 hunk 划入同一区域。
 * 相邻但不相交的修改（一侧改第 2 行、另一侧改第 3 行）保持各自独立，可自动合并。
 */
function groupRegions(oursHunks: Hunk[], theirsHunks: Hunk[]): Region[] {
  const tagged: { hunk: Hunk; side: 0 | 1 }[] = [
    ...oursHunks.map((hunk) => ({ hunk, side: 0 as const })),
    ...theirsHunks.map((hunk) => ({ hunk, side: 1 as const })),
  ];
  tagged.sort((x, y) => x.hunk.baseStart - y.hunk.baseStart || x.side - y.side);

  const regions: Region[] = [];
  for (const { hunk, side } of tagged) {
    const last = regions[regions.length - 1];
    const joins =
      last !== undefined &&
      (hunk.baseStart < last.end ||
        (hunk.baseStart === last.end && last.start === last.end));
    if (joins) {
      last.end = Math.max(last.end, hunk.baseEnd);
      (side === 0 ? last.oursHunks : last.theirsHunks).push(hunk);
    } else {
      regions.push({
        start: hunk.baseStart,
        end: hunk.baseEnd,
        oursHunks: side === 0 ? [hunk] : [],
        theirsHunks: side === 1 ? [hunk] : [],
      });
    }
  }
  return regions;
}

/** 重现某一侧在区域 [start, end) 内的完整内容（hunk 间隙用 base 行填充）。 */
function sideContent(
  baseLines: string[],
  start: number,
  end: number,
  hunks: Hunk[],
): string[] {
  const out: string[] = [];
  let pos = start;
  for (const hunk of hunks) {
    while (pos < hunk.baseStart) out.push(baseLines[pos++]);
    out.push(...hunk.lines);
    pos = hunk.baseEnd;
  }
  while (pos < end) out.push(baseLines[pos++]);
  return out;
}

/** 确保冲突块内每一行都有行尾（标记行不能和内容挤在同一行）。 */
function terminated(lines: string[]): string[] {
  return lines.map((line) => (line.endsWith("\n") ? line : line + "\n"));
}

/**
 * 三路合并入口。
 *
 * 冲突判定规则：
 * 1. 只有一侧修改了某区域 → 采用该侧；
 * 2. 两侧对同一区域做出完全相同的修改 → 采用一份；
 * 3. 两侧修改了相交的 base 行区间，或在同一位置插入不同内容 → 结构化冲突。
 */
export function merge3(
  base: string,
  ours: string,
  theirs: string,
  options: MergeOptions = {},
): MergeResult {
  const eol: EolStyle =
    options.eol === "lf" ? "\n" : options.eol === "crlf" ? "\r\n" : detectEol(base);
  const oursLabel = options.oursLabel ?? "ours";
  const theirsLabel = options.theirsLabel ?? "theirs";

  const baseLines = splitLinesKeepEnds(base);
  const oursHunks = diffHunks(baseLines, splitLinesKeepEnds(ours));
  const theirsHunks = diffHunks(baseLines, splitLinesKeepEnds(theirs));
  const regions = groupRegions(oursHunks, theirsHunks);

  const outLines: string[] = [];
  const conflicts: MergeConflict[] = [];
  // 记录每个冲突在 outLines 中的行区间，join 阶段再换算成 UTF-16 偏移。
  const conflictLineRanges: { startLine: number; endLine: number }[] = [];

  let cursor = 0;
  for (const region of regions) {
    while (cursor < region.start) outLines.push(baseLines[cursor++]);

    const baseContent = baseLines.slice(region.start, region.end);
    const oursContent = sideContent(baseLines, region.start, region.end, region.oursHunks);
    const theirsContent = sideContent(baseLines, region.start, region.end, region.theirsHunks);

    const oursChanged = !linesEqual(oursContent, baseContent);
    const theirsChanged = !linesEqual(theirsContent, baseContent);

    if (!oursChanged && !theirsChanged) {
      outLines.push(...baseContent);
    } else if (!oursChanged) {
      outLines.push(...theirsContent);
    } else if (!theirsChanged || linesEqual(oursContent, theirsContent)) {
      // 仅 ours 修改，或两侧修改完全相同（只保留一份）。
      outLines.push(...oursContent);
    } else {
      const startLine = outLines.length;
      outLines.push(`<<<<<<< ${oursLabel}\n`);
      outLines.push(...terminated(oursContent));
      outLines.push("=======\n");
      outLines.push(...terminated(theirsContent));
      outLines.push(`>>>>>>> ${theirsLabel}\n`);
      conflictLineRanges.push({ startLine, endLine: outLines.length });
      conflicts.push({
        startOffset: 0, // join 阶段回填
        endOffset: 0,
        base: baseContent.map(stripEnd),
        ours: oursContent.map(stripEnd),
        theirs: theirsContent.map(stripEnd),
      });
    }
    cursor = region.end;
  }
  while (cursor < baseLines.length) outLines.push(baseLines[cursor++]);

  // 拼接输出并回填 UTF-16 偏移。偏移全部落在行边界上，不会切开代理对。
  let text = "";
  let offset = 0;
  let conflictIndex = 0;
  for (let lineIndex = 0; lineIndex < outLines.length; lineIndex++) {
    if (
      conflictIndex < conflictLineRanges.length &&
      conflictLineRanges[conflictIndex].startLine === lineIndex
    ) {
      conflicts[conflictIndex].startOffset = offset;
    }
    const piece =
      eol === "\r\n" && outLines[lineIndex].endsWith("\n")
        ? outLines[lineIndex].slice(0, -1) + "\r\n"
        : outLines[lineIndex];
    text += piece;
    offset += piece.length;
    if (
      conflictIndex < conflictLineRanges.length &&
      conflictLineRanges[conflictIndex].endLine === lineIndex + 1
    ) {
      conflicts[conflictIndex].endOffset = offset;
      conflictIndex++;
    }
  }

  return { text, clean: conflicts.length === 0, conflicts, eol };
}

/**
 * 校验 offset 是否落在 text 的合法 UTF-16 边界上（不切开代理对）。
 * 供调用方与测试使用。
 */
export function isUtf16Boundary(text: string, offset: number): boolean {
  if (offset < 0 || offset > text.length) return false;
  const before = text.charCodeAt(offset - 1);
  const after = text.charCodeAt(offset);
  const endsWithHighSurrogate = before >= 0xd800 && before <= 0xdbff;
  const startsWithLowSurrogate = after >= 0xdc00 && after <= 0xdfff;
  return !(endsWithHighSurrogate && startsWithLowSurrogate);
}

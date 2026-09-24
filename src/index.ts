import {
  EMPTY_NODE,
  assertBoundary,
  assertValidText,
  buildFromText,
  concat,
  lastBreakEndRelative,
  locateLeaf,
  nthBreakEnd,
  readNodeRange,
  splitAt,
} from "./rope.ts";
import type { RangeChunk, TextNode } from "./rope.ts";

export type {
  BranchNode,
  LeafNode,
  RangeChunk,
  TextNode,
} from "./rope.ts";

export interface LinePosition {
  readonly line: number;
  readonly column: number;
}

export class ShardedText {
  readonly root: TextNode;

  private constructor(root: TextNode) {
    this.root = root;
  }

  static empty(): ShardedText {
    return new ShardedText(EMPTY_NODE);
  }

  static from(text: string): ShardedText {
    return new ShardedText(buildFromText(text));
  }

  get length(): number {
    return this.root.length;
  }

  get lineBreakCount(): number {
    return this.root.breaks;
  }

  get lineCount(): number {
    return this.root.breaks + 1;
  }

  insert(offset: number, text: string): ShardedText {
    assertBoundary(this.root, offset, "Insert offset");
    assertValidText(text);
    if (text.length === 0) return this;
    const [before, after] = splitAt(this.root, offset);
    return new ShardedText(concat(concat(before, buildFromText(text)), after));
  }

  delete(start: number, end: number): ShardedText {
    this.assertRange(start, end);
    if (start === end) return this;
    const [before] = splitAt(this.root, start);
    const [, after] = splitAt(this.root, end);
    return new ShardedText(concat(before, after));
  }

  replace(start: number, end: number, text: string): ShardedText {
    this.assertRange(start, end);
    assertValidText(text);
    const [before] = splitAt(this.root, start);
    const [, after] = splitAt(this.root, end);
    return new ShardedText(concat(concat(before, buildFromText(text)), after));
  }

  private assertRange(start: number, end: number): void {
    assertBoundary(this.root, start, "Range start");
    assertBoundary(this.root, end, "Range end");
    if (start > end) {
      throw new RangeError(
        `Range start ${start} is greater than range end ${end}.`,
      );
    }
  }

  readRange(start: number, end: number): Generator<RangeChunk> {
    this.assertRange(start, end);
    return readNodeRange(this.root, 0, start, end);
  }

  slice(start: number, end: number): string {
    let result = "";
    for (const chunk of this.readRange(start, end)) {
      result += chunk.text;
    }
    return result;
  }

  toString(): string {
    return this.slice(0, this.length);
  }

  positionAt(offset: number): LinePosition {
    assertBoundary(this.root, offset, "Offset");
    let line = 0;
    let lineStart = 0;
    let nodeStart = 0;
    let node = this.root;
    while (node.kind === "branch") {
      const leftEnd = nodeStart + node.left.length;
      if (offset <= leftEnd) {
        node = node.left;
      } else {
        line += node.left.breaks;
        const breakEnd = lastBreakEndRelative(node.left);
        if (breakEnd >= 0) lineStart = nodeStart + breakEnd;
        nodeStart = leftEnd;
        node = node.right;
      }
    }
    const local = offset - nodeStart;
    for (let index = 0; index < local; index += 1) {
      if (node.text.charCodeAt(index) === 0x0a) {
        line += 1;
        lineStart = nodeStart + index + 1;
      }
    }
    return { line, column: offset - lineStart };
  }

  offsetAt(position: LinePosition): number {
    const { line, column } = position;
    if (!Number.isSafeInteger(line) || line < 0 || line >= this.lineCount) {
      throw new RangeError(
        `Line ${line} is outside the document (line count ${this.lineCount}).`,
      );
    }
    if (!Number.isSafeInteger(column) || column < 0) {
      throw new RangeError(`Column ${column} must be a non-negative integer.`);
    }
    const lineStart = line === 0 ? 0 : nthBreakEnd(this.root, line - 1);
    const lineEnd = this.lineEndOffset(line);
    const offset = lineStart + column;
    if (offset > lineEnd) {
      throw new RangeError(
        `Column ${column} is beyond the end of line ${line}.`,
      );
    }
    assertBoundary(this.root, offset, "Position");
    return offset;
  }

  private lineEndOffset(line: number): number {
    if (line === this.lineCount - 1) return this.length;
    const breakEnd = nthBreakEnd(this.root, line);
    const { node, start } = locateLeaf(this.root, breakEnd - 1);
    const local = breakEnd - 1 - start;
    if (local > 0 && node.text.charCodeAt(local - 1) === 0x0d) {
      return breakEnd - 2;
    }
    return breakEnd - 1;
  }
}

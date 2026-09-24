/**
 * Persistent (immutable, version-preserving) chunked text rope.
 *
 * Design notes (documented semantics):
 * - All public offsets and columns are UTF-16 code-unit offsets (same as
 *   String.prototype.length / LSP "character" units).
 * - Every node caches `length` (UTF-16 code units) and `newlines` (number of
 *   "\n" in its subtree). A CRLF pair therefore counts as exactly one line
 *   break; a lone "\r" counts as none.
 * - Edit boundaries (insert offset, delete/replace start & end) must not fall
 *   *between* the "\r" and "\n" of a CRLF pair, and must not split a UTF-16
 *   surrogate pair. Such edits are REJECTED with a RangeError (this library
 *   chooses rejection over silent splitting/re-indexing). Reading and
 *   position conversion remain total functions: any offset in [0, length]
 *   can be converted to a line/column position and back.
 * - All mutating operations return a new tree that shares every untouched
 *   node with the previous version; old versions are never modified, so
 *   reads on any version can never pollute another version.
 * - Range reads are exposed as a chunk generator, so callers never need to
 *   concatenate the whole document to read a slice.
 */

export const MAX_LEAF_SIZE = 1024;

export interface Leaf {
  readonly kind: "leaf";
  readonly text: string;
  /** UTF-16 code-unit length of this subtree. */
  readonly length: number;
  /** Number of "\n" in this subtree (CRLF counts once). */
  readonly newlines: number;
}

export interface Branch {
  readonly kind: "branch";
  readonly left: TextNode;
  readonly right: TextNode;
  readonly length: number;
  readonly newlines: number;
  readonly height: number;
}

export type TextNode = Leaf | Branch;

export interface Position {
  /** 0-based line index. */
  readonly line: number;
  /** 0-based UTF-16 code-unit column within the line. */
  readonly character: number;
}

function countLf(text: string): number {
  let count = 0;
  for (let i = 0; i < text.length; i++) {
    if (text.charCodeAt(i) === 0x0a) count++;
  }
  return count;
}

function makeLeaf(text: string): Leaf {
  return { kind: "leaf", text, length: text.length, newlines: countLf(text) };
}

function makeBranch(left: TextNode, right: TextNode): Branch {
  return {
    kind: "branch",
    left,
    right,
    length: left.length + right.length,
    newlines: left.newlines + right.newlines,
    height: 1 + Math.max(nodeHeight(left), nodeHeight(right)),
  };
}

function nodeHeight(node: TextNode): number {
  return node.kind === "branch" ? node.height : 0;
}

const EMPTY_LEAF: Leaf = makeLeaf("");

/** Builds a balanced tree from `text`, chunking into <= MAX_LEAF_SIZE leaves. */
export function build(text: string): TextNode {
  if (text.length === 0) return EMPTY_LEAF;
  const chunks: string[] = [];
  let start = 0;
  while (start < text.length) {
    let end = Math.min(start + MAX_LEAF_SIZE, text.length);
    if (end < text.length && end - 1 > start) {
      const before = text.charCodeAt(end - 1);
      const after = text.charCodeAt(end);
      const splitsSurrogate =
        before >= 0xd800 && before <= 0xdbff && after >= 0xdc00 && after <= 0xdfff;
      const splitsCrlf = before === 0x0d && after === 0x0a;
      if (splitsSurrogate || splitsCrlf) end -= 1;
    }
    chunks.push(text.slice(start, end));
    start = end;
  }
  let level: TextNode[] = chunks.map(makeLeaf);
  while (level.length > 1) {
    const next: TextNode[] = [];
    for (let i = 0; i < level.length; i += 2) {
      next.push(i + 1 < level.length ? makeBranch(level[i], level[i + 1]) : level[i]);
    }
    level = next;
  }
  return level[0];
}

function rotate(left: TextNode, right: TextNode): TextNode {
  const hl = nodeHeight(left);
  const hr = nodeHeight(right);
  if (hl > hr + 1 && left.kind === "branch") {
    if (nodeHeight(left.left) >= nodeHeight(left.right)) {
      return makeBranch(left.left, makeBranch(left.right, right));
    }
    const mid = left.right as Branch;
    return makeBranch(makeBranch(left.left, mid.left), makeBranch(mid.right, right));
  }
  if (hr > hl + 1 && right.kind === "branch") {
    if (nodeHeight(right.right) >= nodeHeight(right.left)) {
      return makeBranch(makeBranch(left, right.left), right.right);
    }
    const mid = right.left as Branch;
    return makeBranch(makeBranch(left, mid.left), makeBranch(mid.right, right.right));
  }
  return makeBranch(left, right);
}

/** AVL-style O(log n) concatenation of two trees. */
function concat(a: TextNode | null, b: TextNode | null): TextNode | null {
  if (a === null) return b;
  if (b === null) return a;
  const diff = nodeHeight(a) - nodeHeight(b);
  if (diff >= -1 && diff <= 1) return makeBranch(a, b);
  if (diff > 1 && a.kind === "branch") {
    return rotate(a.left, concat(a.right, b) as TextNode);
  }
  if (diff < -1 && b.kind === "branch") {
    return rotate(concat(a, b.left) as TextNode, b.right);
  }
  return makeBranch(a, b);
}

/** Splits into [first `offset` code units, remainder]; either side may be null. */
function split(node: TextNode, offset: number): [TextNode | null, TextNode | null] {
  if (offset <= 0) return [null, node];
  if (offset >= node.length) return [node, null];
  if (node.kind === "leaf") {
    return [makeLeaf(node.text.slice(0, offset)), makeLeaf(node.text.slice(offset))];
  }
  if (offset < node.left.length) {
    const [l, r] = split(node.left, offset);
    return [l, concat(r, node.right)];
  }
  const [l, r] = split(node.right, offset - node.left.length);
  return [concat(node.left, l), r];
}

/** Returns the single UTF-16 code unit at `offset` as a one-char string. */
export function charAt(root: TextNode, offset: number): string {
  let node = root;
  let i = offset;
  while (node.kind === "branch") {
    if (i < node.left.length) {
      node = node.left;
    } else {
      i -= node.left.length;
      node = node.right;
    }
  }
  return node.text[i];
}

function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

/**
 * Validates an edit boundary. Rejects (RangeError):
 * - offsets outside [0, length];
 * - offsets between a high and a low surrogate (would split a surrogate pair);
 * - offsets strictly between "\r" and "\n" (would split a CRLF pair).
 */
export function assertEditBoundary(root: TextNode, offset: number, what: string): void {
  if (!Number.isInteger(offset) || offset < 0 || offset > root.length) {
    throw new RangeError(
      `${what}: offset ${offset} out of range [0, ${root.length}]`,
    );
  }
  const before = offset > 0 ? charAt(root, offset - 1).charCodeAt(0) : -1;
  const after = offset < root.length ? charAt(root, offset).charCodeAt(0) : -1;
  if (isHighSurrogate(before) && isLowSurrogate(after)) {
    throw new RangeError(
      `${what}: offset ${offset} splits a UTF-16 surrogate pair`,
    );
  }
  if (before === 0x0d && after === 0x0a) {
    throw new RangeError(
      `${what}: offset ${offset} falls inside a CRLF line break; ` +
        `insert/delete at the boundary before "\\r" or after "\\n" instead`,
    );
  }
}

/** Returns a new tree with `text` inserted at UTF-16 `offset`. */
export function insertText(root: TextNode, offset: number, text: string): TextNode {
  assertEditBoundary(root, offset, "insert");
  if (text.length === 0) return root;
  const [left, right] = split(root, offset);
  return concat(concat(left, build(text)), right) as TextNode;
}

/** Returns a new tree with the half-open range [start, end) removed. */
export function deleteRange(root: TextNode, start: number, end: number): TextNode {
  if (!Number.isInteger(start) || !Number.isInteger(end) || start > end) {
    throw new RangeError(`delete: invalid range [${start}, ${end})`);
  }
  if (start === end) {
    assertEditBoundary(root, start, "delete");
    return root;
  }
  assertEditBoundary(root, start, "delete start");
  assertEditBoundary(root, end, "delete end");
  const [left, rest] = split(root, start);
  const [, right] = split(rest as TextNode, end - start);
  return concat(left, right) ?? EMPTY_LEAF;
}

/** Returns a new tree replacing [start, end) with `text`. */
export function replaceRange(
  root: TextNode,
  start: number,
  end: number,
  text: string,
): TextNode {
  if (!Number.isInteger(start) || !Number.isInteger(end) || start > end) {
    throw new RangeError(`replace: invalid range [${start}, ${end})`);
  }
  assertEditBoundary(root, start, "replace start");
  assertEditBoundary(root, end, "replace end");
  if (start === end && text.length === 0) return root;
  const [left, rest] = split(root, start);
  const [, right] = split(rest as TextNode, end - start);
  const middle = text.length === 0 ? null : build(text);
  return concat(concat(left, middle), right) ?? EMPTY_LEAF;
}

function countNewlinesBefore(node: TextNode, offset: number): number {
  if (node.kind === "leaf") {
    let count = 0;
    const limit = Math.min(offset, node.text.length);
    for (let i = 0; i < limit; i++) {
      if (node.text.charCodeAt(i) === 0x0a) count++;
    }
    return count;
  }
  if (offset <= node.left.length) return countNewlinesBefore(node.left, offset);
  return node.left.newlines + countNewlinesBefore(node.right, offset - node.left.length);
}

/** Offset of the k-th (0-based) "\n"; requires 0 <= k < root.newlines. */
function offsetOfKthNewline(node: TextNode, k: number): number {
  if (node.kind === "leaf") {
    let index = -1;
    for (let seen = 0; seen <= k; seen++) {
      index = node.text.indexOf("\n", index + 1);
    }
    return index;
  }
  if (k < node.left.newlines) return offsetOfKthNewline(node.left, k);
  return node.left.length + offsetOfKthNewline(node.right, k - node.left.newlines);
}

/** Converts a UTF-16 offset to a 0-based line/column position. */
export function offsetToPosition(root: TextNode, offset: number): Position {
  if (!Number.isInteger(offset) || offset < 0 || offset > root.length) {
    throw new RangeError(`positionAt: offset ${offset} out of range [0, ${root.length}]`);
  }
  const line = countNewlinesBefore(root, offset);
  const lineStart = line === 0 ? 0 : offsetOfKthNewline(root, line - 1) + 1;
  return { line, character: offset - lineStart };
}

/**
 * Converts a 0-based line/column position back to a UTF-16 offset.
 * `character` is measured in UTF-16 code units from the line start and may
 * address the end of the line (the "\n" itself, or end of document).
 */
export function positionToOffset(root: TextNode, position: Position): number {
  const { line, character } = position;
  if (!Number.isInteger(line) || !Number.isInteger(character) || line < 0 || character < 0) {
    throw new RangeError(`offsetAt: invalid position ${JSON.stringify(position)}`);
  }
  if (line > root.newlines) {
    throw new RangeError(`offsetAt: line ${line} out of range [0, ${root.newlines}]`);
  }
  const lineStart = line === 0 ? 0 : offsetOfKthNewline(root, line - 1) + 1;
  const lineEnd = line < root.newlines ? offsetOfKthNewline(root, line) : root.length;
  const offset = lineStart + character;
  if (offset > lineEnd) {
    throw new RangeError(
      `offsetAt: character ${character} exceeds line ${line} length ${lineEnd - lineStart}`,
    );
  }
  return offset;
}

/**
 * Yields the text of [start, end) as leaf-aligned chunks. Never builds the
 * full document string; untouched subtrees are skipped in O(log n).
 */
export function iterateRange(
  root: TextNode,
  start: number,
  end: number,
): Generator<string, void, undefined> {
  if (!Number.isInteger(start) || !Number.isInteger(end) || start < 0 || end > root.length || start > end) {
    throw new RangeError(`iterateRange: invalid range [${start}, ${end})`);
  }
  return sliceNode(root, start, end, 0);
}

function* sliceNode(
  node: TextNode,
  start: number,
  end: number,
  base: number,
): Generator<string, void, undefined> {
  if (start >= end) return;
  if (node.kind === "leaf") {
    yield node.text.slice(start - base, end - base);
    return;
  }
  const leftEnd = base + node.left.length;
  if (start < leftEnd) {
    yield* sliceNode(node.left, start, Math.min(end, leftEnd), base);
  }
  if (end > leftEnd) {
    yield* sliceNode(node.right, Math.max(start, leftEnd), end, leftEnd);
  }
}

/** Convenience full-text read (joins all chunks; prefer iterateRange for slices). */
export function getText(root: TextNode): string {
  let out = "";
  for (const chunk of iterateRange(root, 0, root.length)) out += chunk;
  return out;
}

/**
 * Immutable persistent text document. Every mutating method returns a new
 * TextDocument that shares unchanged nodes with the receiver; the receiver
 * is never modified.
 */
export class TextDocument {
  readonly root: TextNode;

  private constructor(root: TextNode) {
    this.root = root;
  }

  static from(text: string): TextDocument {
    return new TextDocument(build(text));
  }

  static empty(): TextDocument {
    return new TextDocument(EMPTY_LEAF);
  }

  /** Total length in UTF-16 code units. */
  get length(): number {
    return this.root.length;
  }

  /** Number of lines (newline count + 1). */
  get lineCount(): number {
    return this.root.newlines + 1;
  }

  insert(offset: number, text: string): TextDocument {
    return new TextDocument(insertText(this.root, offset, text));
  }

  delete(start: number, end: number): TextDocument {
    return new TextDocument(deleteRange(this.root, start, end));
  }

  replace(start: number, end: number, text: string): TextDocument {
    return new TextDocument(replaceRange(this.root, start, end, text));
  }

  positionAt(offset: number): Position {
    return offsetToPosition(this.root, offset);
  }

  offsetAt(position: Position): number {
    return positionToOffset(this.root, position);
  }

  charAt(offset: number): string {
    if (offset < 0 || offset >= this.root.length) {
      throw new RangeError(`charAt: offset ${offset} out of range`);
    }
    return charAt(this.root, offset);
  }

  /** Iterates [start, end) as chunks without concatenating the document. */
  chunks(start = 0, end = this.root.length): Generator<string, void, undefined> {
    return iterateRange(this.root, start, end);
  }

  getText(): string {
    return getText(this.root);
  }
}

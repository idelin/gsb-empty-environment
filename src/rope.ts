export const TARGET_LEAF_LENGTH = 128;

export interface LeafNode {
  readonly kind: "leaf";
  readonly text: string;
  readonly length: number;
  readonly breaks: number;
  readonly height: 1;
}

export interface BranchNode {
  readonly kind: "branch";
  readonly left: TextNode;
  readonly right: TextNode;
  readonly length: number;
  readonly breaks: number;
  readonly height: number;
}

export type TextNode = LeafNode | BranchNode;

export interface RangeChunk {
  readonly text: string;
  readonly start: number;
  readonly end: number;
}

export function leaf(text: string): LeafNode {
  let breaks = 0;
  for (let index = 0; index < text.length; index += 1) {
    if (text.charCodeAt(index) === 0x0a) breaks += 1;
  }
  const node: LeafNode = {
    kind: "leaf",
    text,
    length: text.length,
    breaks,
    height: 1,
  };
  return Object.freeze(node);
}

export const EMPTY_NODE: TextNode = leaf("");

function branch(left: TextNode, right: TextNode): BranchNode {
  const node: BranchNode = {
    kind: "branch",
    left,
    right,
    length: left.length + right.length,
    breaks: left.breaks + right.breaks,
    height: 1 + Math.max(left.height, right.height),
  };
  return Object.freeze(node);
}

function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

export function assertValidText(text: string): void {
  for (let index = 0; index < text.length; index += 1) {
    const code = text.charCodeAt(index);
    if (code === 0x0d) {
      if (text.charCodeAt(index + 1) !== 0x0a) {
        throw new Error("Lone CR is not supported; use LF or CRLF line endings.");
      }
      index += 1;
    } else if (isHighSurrogate(code)) {
      if (!isLowSurrogate(text.charCodeAt(index + 1))) {
        throw new Error("Text contains an unpaired UTF-16 high surrogate.");
      }
      index += 1;
    } else if (isLowSurrogate(code)) {
      throw new Error("Text contains an unpaired UTF-16 low surrogate.");
    }
  }
}

function rotateLeft(node: BranchNode): TextNode {
  const right = node.right;
  if (right.kind !== "branch") return node;
  return branch(branch(node.left, right.left), right.right);
}

function rotateRight(node: BranchNode): TextNode {
  const left = node.left;
  if (left.kind !== "branch") return node;
  return branch(left.left, branch(left.right, node.right));
}

function rebalance(node: BranchNode): TextNode {
  const factor = node.left.height - node.right.height;
  if (factor > 1) {
    let left = node.left;
    if (left.kind === "branch" && left.right.height > left.left.height) {
      left = rotateLeft(left);
    }
    return rotateRight(branch(left, node.right));
  }
  if (factor < -1) {
    let right = node.right;
    if (right.kind === "branch" && right.left.height > right.right.height) {
      right = rotateRight(right);
    }
    return rotateLeft(branch(node.left, right));
  }
  return node;
}

export function concat(left: TextNode, right: TextNode): TextNode {
  if (left.length === 0) return right;
  if (right.length === 0) return left;
  if (
    left.kind === "leaf" &&
    right.kind === "leaf" &&
    left.length + right.length <= TARGET_LEAF_LENGTH
  ) {
    return leaf(left.text + right.text);
  }
  if (left.kind === "branch" && left.height > right.height + 1) {
    return rebalance(branch(left.left, concat(left.right, right)));
  }
  if (right.kind === "branch" && right.height > left.height + 1) {
    return rebalance(branch(concat(left, right.left), right.right));
  }
  return branch(left, right);
}

function safeSplitPoint(text: string, offset: number): number {
  const previous = text.charCodeAt(offset - 1);
  const current = text.charCodeAt(offset);
  const splitsSurrogatePair =
    isHighSurrogate(previous) && isLowSurrogate(current);
  const splitsCrlf = previous === 0x0d && current === 0x0a;
  if (!splitsSurrogatePair && !splitsCrlf) return offset;
  return offset + 1 >= text.length ? offset - 1 : offset + 1;
}

export function splitAt(node: TextNode, offset: number): [TextNode, TextNode] {
  if (offset <= 0) return [EMPTY_NODE, node];
  if (offset >= node.length) return [node, EMPTY_NODE];
  if (node.kind === "leaf") {
    return [leaf(node.text.slice(0, offset)), leaf(node.text.slice(offset))];
  }
  if (offset < node.left.length) {
    const [before, inside] = splitAt(node.left, offset);
    return [before, concat(inside, node.right)];
  }
  if (offset > node.left.length) {
    const [inside, after] = splitAt(
      node.right,
      offset - node.left.length,
    );
    return [concat(node.left, inside), after];
  }
  return [node.left, node.right];
}

function buildBalanced(chunks: string[], low: number, high: number): TextNode {
  if (high - low === 1) return leaf(chunks[low] as string);
  const middle = (low + high) >> 1;
  return branch(
    buildBalanced(chunks, low, middle),
    buildBalanced(chunks, middle, high),
  );
}

export function buildFromText(text: string): TextNode {
  assertValidText(text);
  if (text.length === 0) return EMPTY_NODE;
  const chunks: string[] = [];
  let start = 0;
  while (text.length - start > TARGET_LEAF_LENGTH) {
    const end = safeSplitPoint(text, start + TARGET_LEAF_LENGTH);
    chunks.push(text.slice(start, end));
    start = end;
  }
  chunks.push(text.slice(start));
  return buildBalanced(chunks, 0, chunks.length);
}

export function assertBoundary(
  root: TextNode,
  offset: number,
  label: string,
): void {
  if (!Number.isSafeInteger(offset) || offset < 0 || offset > root.length) {
    throw new RangeError(
      `${label} ${offset} is outside the document (length ${root.length}).`,
    );
  }
  if (offset === 0 || offset === root.length) return;
  let node = root;
  let start = 0;
  while (node.kind === "branch") {
    const leftEnd = start + node.left.length;
    if (offset < leftEnd) {
      node = node.left;
    } else if (offset > leftEnd) {
      start = leftEnd;
      node = node.right;
    } else {
      return;
    }
  }
  const local = offset - start;
  if (local === 0 || local === node.length) return;
  const previous = node.text.charCodeAt(local - 1);
  const current = node.text.charCodeAt(local);
  if (isHighSurrogate(previous) && isLowSurrogate(current)) {
    throw new Error(
      `${label} ${offset} splits a UTF-16 surrogate pair; public offsets are UTF-16 code-unit boundaries.`,
    );
  }
  if (previous === 0x0d && current === 0x0a) {
    throw new Error(
      `${label} ${offset} falls inside a CRLF line ending; move the boundary before CR or after LF and retry.`,
    );
  }
}

export function locateLeaf(
  root: TextNode,
  offset: number,
): { node: LeafNode; start: number } {
  let node = root;
  let start = 0;
  while (node.kind === "branch") {
    if (offset < start + node.left.length) {
      node = node.left;
    } else {
      start += node.left.length;
      node = node.right;
    }
  }
  return { node, start };
}

export function nthBreakEnd(root: TextNode, index: number): number {
  let node = root;
  let start = 0;
  let remaining = index;
  while (node.kind === "branch") {
    if (remaining < node.left.breaks) {
      node = node.left;
    } else {
      remaining -= node.left.breaks;
      start += node.left.length;
      node = node.right;
    }
  }
  for (let indexInLeaf = 0; indexInLeaf < node.text.length; indexInLeaf += 1) {
    if (node.text.charCodeAt(indexInLeaf) === 0x0a) {
      if (remaining === 0) return start + indexInLeaf + 1;
      remaining -= 1;
    }
  }
  throw new Error(`Line break ${index} does not exist.`);
}

export function lastBreakEndRelative(node: TextNode): number {
  if (node.kind === "leaf") {
    const index = node.text.lastIndexOf("\n");
    return index < 0 ? -1 : index + 1;
  }
  const right = lastBreakEndRelative(node.right);
  if (right >= 0) return node.left.length + right;
  return lastBreakEndRelative(node.left);
}

export function* readNodeRange(
  node: TextNode,
  nodeStart: number,
  start: number,
  end: number,
): Generator<RangeChunk> {
  const nodeEnd = nodeStart + node.length;
  if (nodeEnd <= start || nodeStart >= end) return;
  if (node.kind === "leaf") {
    const chunkStart = Math.max(start, nodeStart);
    const chunkEnd = Math.min(end, nodeEnd);
    if (chunkStart < chunkEnd) {
      yield {
        text: node.text.slice(chunkStart - nodeStart, chunkEnd - nodeStart),
        start: chunkStart,
        end: chunkEnd,
      };
    }
    return;
  }
  yield* readNodeRange(node.left, nodeStart, start, end);
  yield* readNodeRange(
    node.right,
    nodeStart + node.left.length,
    start,
    end,
  );
}

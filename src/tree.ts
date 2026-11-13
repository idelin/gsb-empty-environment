import { parseFragment } from "./parser.ts";
import type { ChildNode, Node } from "./nodes.ts";

export interface ElementTree {
  tag: string;
  attributes: Record<string, string>;
  children: ElementTree[];
}

export interface TreeDiff {
  equal: boolean;
  added: ElementTree[];
  removed: ElementTree[];
  beforeCount: number;
  afterCount: number;
}

export type TreeInput =
  | string
  | Node
  | ElementTree
  | ElementTree[]
  | ChildNode[];

function isElementTree(value: unknown): value is ElementTree {
  return (
    typeof value === "object" &&
    value !== null &&
    (typeof (value as ElementTree).tag === "string" ||
      typeof (value as ElementTree & { tagName?: string }).tagName ===
        "string") &&
    Array.isArray((value as ElementTree).children)
  );
}

function elementToTree(node: Node | Record<string, unknown>): ElementTree {
  const candidate = node as Node & {
    tag?: string;
    tagName?: string;
    attrs?: Array<{ name: string; value: string }>;
    attributes?: Record<string, string>;
    childNodes?: ChildNode[];
    children?: ChildNode[];
  };

  const tag =
    typeof candidate.tag === "string"
      ? candidate.tag
      : typeof candidate.tagName === "string"
        ? candidate.tagName
        : null;
  if (tag === null) {
    throw new TypeError("Expected an element node");
  }

  const attributes: Record<string, string> = {};
  if (candidate.attributes !== undefined) {
    for (const [name, value] of Object.entries(candidate.attributes)) {
      attributes[name] = String(value);
    }
  } else if (Array.isArray(candidate.attrs)) {
    for (const attr of candidate.attrs) {
      if (!(attr.name in attributes)) {
        attributes[attr.name] = attr.value;
      }
    }
  }

  const tree: ElementTree = {
    tag,
    attributes,
    children: [],
  };

  Object.defineProperty(tree, "tagName", {
    get() {
      return this.tag;
    },
    enumerable: false,
  });

  tree.children = elementChildrenToTree(
    candidate.childNodes ??
      (candidate.children as ChildNode[] | undefined) ??
      [],
  );
  return tree;
}

function elementChildrenToTree(nodes: ChildNode[]): ElementTree[] {
  const output: ElementTree[] = [];
  for (const node of nodes) {
    if (node.type === "element") {
      output.push(elementToTree(node));
    }
  }
  return output;
}

export function toElementTree(input: TreeInput): ElementTree[] {
  if (typeof input === "string") {
    return elementChildrenToTree(parseFragment(input).childNodes);
  }

  if (Array.isArray(input)) {
    if (
      input.every(
        (item) => typeof item === "object" && item !== null && "type" in item,
      )
    ) {
      return elementChildrenToTree(input as ChildNode[]);
    }
    return input.flatMap((item) => toElementTree(item as ElementTree));
  }

  if (isElementTree(input)) {
    const candidate = input as ElementTree & { tagName?: string };
    if (typeof candidate.tag !== "string" && candidate.tagName !== undefined) {
      return [
        {
          tag: candidate.tagName,
          attributes: candidate.attributes ?? {},
          children: candidate.children,
        },
      ];
    }
    return [input];
  }

  if (
    (input as Node).type === "element" ||
    typeof (input as { tagName?: string }).tagName === "string"
  ) {
    return [elementToTree(input)];
  }

  return elementChildrenToTree(input.childNodes);
}

export const elementTree = toElementTree;
export const getElementTree = toElementTree;

function attributesEqual(
  left: Record<string, string>,
  right: Record<string, string>,
): boolean {
  const leftKeys = Object.keys(left).sort();
  const rightKeys = Object.keys(right).sort();
  if (leftKeys.length !== rightKeys.length) {
    return false;
  }
  for (let index = 0; index < leftKeys.length; index += 1) {
    const key = leftKeys[index];
    if (key !== rightKeys[index] || left[key] !== right[key]) {
      return false;
    }
  }
  return true;
}

export interface CompareOptions {
  ignoreAttributes?: boolean;
  ignoreRootTag?: boolean;
  ignoreCase?: boolean;
  ignoreText?: boolean;
}

function treesEqual(
  left: ElementTree,
  right: ElementTree,
  options: CompareOptions,
): boolean {
  const leftTag = options.ignoreCase === true ? left.tag.toLowerCase() : left.tag;
  const rightTag =
    options.ignoreCase === true ? right.tag.toLowerCase() : right.tag;

  if (leftTag !== rightTag) {
    return false;
  }

  if (
    options.ignoreAttributes !== true &&
    !attributesEqual(left.attributes, right.attributes)
  ) {
    return false;
  }

  if (left.children.length !== right.children.length) {
    return false;
  }

  for (let index = 0; index < left.children.length; index += 1) {
    if (
      !treesEqual(left.children[index], right.children[index], options)
    ) {
      return false;
    }
  }

  return true;
}

export function compareElementTrees(
  leftInput: TreeInput,
  rightInput: TreeInput,
  options: CompareOptions = {},
): boolean {
  const left = toElementTree(leftInput);
  const right = toElementTree(rightInput);

  if (options.ignoreRootTag === true) {
    const leftChildren =
      left.length === 1 ? left[0].children : left;
    const rightChildren =
      right.length === 1 ? right[0].children : right;
    if (leftChildren.length !== rightChildren.length) {
      return false;
    }
    return leftChildren.every((child, index) =>
      treesEqual(child, rightChildren[index], options),
    );
  }

  if (left.length !== right.length) {
    return false;
  }
  return left.every((tree, index) =>
    treesEqual(tree, right[index], options),
  );
}

export const compareElementTree = compareElementTrees;
export const elementTreesEqual = compareElementTrees;

export function comparePayloadElementTrees(
  leftInput: TreeInput,
  rightInput: TreeInput,
  options: CompareOptions = {},
): boolean {
  return compareElementTrees(leftInput, rightInput, {
    ignoreRootTag: true,
    ...options,
  });
}

function pathSignature(path: string[], attributes: Record<string, string>): string {
  const attrs = Object.keys(attributes)
    .sort()
    .map((name) => `${name}=${attributes[name]}`)
    .join("\u0001");
  return `${path.join("\u0000")}\u0002${attrs}`;
}

interface IndexedTree {
  tree: ElementTree;
  signature: string;
  path: string[];
}

function indexTrees(
  trees: ElementTree[],
  parentPath: string[] = [],
): IndexedTree[] {
  const output: IndexedTree[] = [];
  for (const tree of trees) {
    const path = [...parentPath, tree.tag];
    output.push({
      tree,
      signature: pathSignature(path, tree.attributes),
      path,
    });
    output.push(...indexTrees(tree.children, path));
  }
  return output;
}

function multisetDifference(
  left: IndexedTree[],
  right: IndexedTree[],
): IndexedTree[] {
  const counts = new Map<string, number>();
  for (const item of left) {
    counts.set(item.signature, (counts.get(item.signature) ?? 0) + 1);
  }

  const output: IndexedTree[] = [];
  for (const item of right) {
    const count = counts.get(item.signature) ?? 0;
    if (count > 0) {
      counts.set(item.signature, count - 1);
    } else {
      output.push(item);
    }
  }
  return output;
}

export function diffElementTrees(
  leftInput: TreeInput,
  rightInput: TreeInput,
): TreeDiff {
  const left = indexTrees(toElementTree(leftInput));
  const right = indexTrees(toElementTree(rightInput));
  const added = multisetDifference(left, right);
  const removed = multisetDifference(right, left);

  return {
    equal: added.length === 0 && removed.length === 0,
    added: added.map((item) => item.tree),
    removed: removed.map((item) => item.tree),
    beforeCount: left.length,
    afterCount: right.length,
  };
}

export function hasNewElements(
  leftInput: TreeInput,
  rightInput: TreeInput,
): boolean {
  return diffElementTrees(leftInput, rightInput).added.length > 0;
}

export const compareElementTreeStructure = compareElementTrees;

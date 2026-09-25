import type { PathInterner, PathNode } from "./path.ts";
import { pathToArray } from "./path.ts";
import type { JsonValue, PathRecord, Scalar } from "./record.ts";

/** Immutable committed tree. Versions never mutate after publication. */
export type TreeNode =
  | { kind: "obj"; children: Map<string, TreeNode> }
  | { kind: "arr"; slots: (TreeNode | undefined)[] }
  | { kind: "val"; v: Scalar };

export function lookupNode(root: TreeNode, path: PathNode): TreeNode | undefined {
  let cur = root;
  for (const seg of pathToArray(path)) {
    if (cur.kind === "obj" && seg.t === "k") {
      const next = cur.children.get(seg.k);
      if (next === undefined) return undefined;
      cur = next;
    } else if (cur.kind === "arr" && seg.t === "i") {
      const next = cur.slots[seg.i];
      if (next === undefined) return undefined;
      cur = next;
    } else {
      return undefined;
    }
  }
  return cur;
}

/** Iterative sorted record stream for a committed subtree. */
export function* nodeToRecords(
  root: TreeNode,
  interner: PathInterner,
  base: PathNode,
): Generator<PathRecord> {
  const stack: { path: PathNode; node: TreeNode }[] = [{ path: base, node: root }];
  while (stack.length > 0) {
    const { path, node } = stack.pop() as { path: PathNode; node: TreeNode };
    if (node.kind === "obj") {
      yield { path, op: "obj" };
      const keys = [...node.children.keys()].sort();
      for (let i = keys.length - 1; i >= 0; i--) {
        stack.push({
          path: interner.intern(path, { t: "k", k: keys[i] }),
          node: node.children.get(keys[i]) as TreeNode,
        });
      }
    } else if (node.kind === "arr") {
      yield { path, op: "arr", n: node.slots.length };
      for (let i = node.slots.length - 1; i >= 0; i--) {
        const child = node.slots[i];
        if (child !== undefined) {
          stack.push({ path: interner.intern(path, { t: "i", i }), node: child });
        }
      }
    } else {
      yield { path, op: "val", v: node.v };
    }
  }
}

function define(out: object, key: string, value: JsonValue): void {
  Object.defineProperty(out, key, {
    value,
    enumerable: true,
    writable: true,
    configurable: true,
  });
}

/** Iterative materialization back to a plain JSON value; preserves holes. */
export function nodeToJson(root: TreeNode): JsonValue {
  if (root.kind === "val") return root.v;
  const rootOut = root.kind === "arr" ? new Array(root.slots.length) : {};
  const stack: { node: TreeNode; out: JsonValue }[] = [{ node: root, out: rootOut }];
  while (stack.length > 0) {
    const { node, out } = stack.pop() as { node: TreeNode; out: JsonValue };
    if (node.kind === "obj") {
      for (const [key, child] of node.children) {
        if (child.kind === "val") {
          define(out, key, child.v);
        } else {
          const childOut = child.kind === "arr" ? new Array(child.slots.length) : {};
          define(out, key, childOut as JsonValue);
          stack.push({ node: child, out: childOut as JsonValue });
        }
      }
    } else if (node.kind === "arr") {
      const arr = out as JsonValue[];
      for (let i = 0; i < node.slots.length; i++) {
        const child = node.slots[i];
        if (child === undefined) continue;
        if (child.kind === "val") {
          arr[i] = child.v;
        } else {
          const childOut = child.kind === "arr" ? new Array(child.slots.length) : {};
          arr[i] = childOut as JsonValue;
          stack.push({ node: child, out: childOut as JsonValue });
        }
      }
    }
  }
  return rootOut as JsonValue;
}

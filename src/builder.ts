import { PathError } from "./errors.ts";
import type { TreeNode } from "./node.ts";
import { formatPath, pathToArray, type PathNode } from "./path.ts";
import { canonical, type PathRecord } from "./record.ts";
import type { SourcedRecord } from "./store.ts";

export interface BuildResult {
  root: TreeNode;
  count: number;
}

interface Frame {
  path: PathNode;
  node: TreeNode;
}

function fail(
  kind: ConstructorParameters<typeof PathError>[0],
  at: PathNode,
  msg: string,
  origin: string,
): never {
  throw new PathError(kind, pathToArray(at), `${msg} at ${formatPath(pathToArray(at))}`, origin);
}

/**
 * Validates and builds a tree from a path-sorted record stream in a single
 * streaming pass. Records are consumed as they arrive (never fully
 * materialized by the caller first) and the frame stack is O(depth), so
 * 100k-deep trees are handled iteratively.
 *
 * Checks, in stream order:
 *  - duplicate definitions (identical duplicates are idempotently merged,
 *    conflicting ones are an error located at the path),
 *  - reference integrity (every non-root record's parent must be defined),
 *  - parent/child type agreement (scalar parents reject children, object
 *    parents require key segments, array parents require in-range indexes).
 */
export async function buildTree(records: AsyncIterable<SourcedRecord>): Promise<BuildResult> {
  const stack: Frame[] = [];
  let root: TreeNode | undefined;
  let count = 0;
  let lastPath: PathNode | undefined;
  let lastCanonical = "";

  for await (const { rec, origin } of records) {
    if (rec.op === "del") {
      fail("UnexpectedDelete", rec.path, "tombstone record in a full build", origin);
    }
    if (lastPath !== undefined && rec.path === lastPath) {
      if (canonical(rec) === lastCanonical) continue; // idempotent re-delivery
      fail("DuplicateDefinition", rec.path, "conflicting definitions for the same path", origin);
    }
    lastPath = rec.path;
    lastCanonical = canonical(rec);

    while (stack.length > 0 && (stack[stack.length - 1] as Frame).path.depth >= rec.path.depth) {
      stack.pop();
    }

    let node: TreeNode;
    switch (rec.op) {
      case "obj":
        node = { kind: "obj", children: new Map() };
        break;
      case "arr":
        node = { kind: "arr", slots: new Array(rec.n).fill(undefined) };
        break;
      case "val":
        node = { kind: "val", v: rec.v };
        break;
    }

    if (rec.path.depth === 0) {
      if (root !== undefined) fail("DuplicateDefinition", rec.path, "multiple root records", origin);
      root = node;
    } else {
      const parent = stack.length > 0 ? (stack[stack.length - 1] as Frame) : undefined;
      if (parent === undefined || parent.path.depth !== rec.path.depth - 1) {
        fail("MissingParent", rec.path, "no record defines the parent path", origin);
      }
      if (parent.path !== rec.path.parent) {
        fail("MissingParent", rec.path, "no record defines the parent path", origin);
      }
      attach(parent, rec, node, origin);
    }
    count++;
    stack.push({ path: rec.path, node });
  }

  if (root === undefined) {
    throw new PathError("MissingRoot", [], "record stream is empty: no root record at $");
  }
  return { root, count };
}

function attach(parent: Frame, rec: PathRecord, child: TreeNode, origin: string): void {
  const seg = rec.path.seg;
  if (seg === null) return;
  const p = parent.node;
  if (p.kind === "val") {
    fail("ParentTypeConflict", rec.path, "parent is a scalar but a child path was defined", origin);
  }
  if (p.kind === "obj") {
    if (seg.t !== "k") {
      fail(
        "SegmentTypeMismatch",
        rec.path,
        "object parent requires a string key segment, not an array index",
        origin,
      );
    }
    p.children.set(seg.k, child);
    return;
  }
  if (seg.t !== "i") {
    fail(
      "SegmentTypeMismatch",
      rec.path,
      "array parent requires an index segment, not a string key",
      origin,
    );
  }
  if (seg.i >= p.slots.length) {
    fail(
      "IndexOutOfRange",
      rec.path,
      `index ${seg.i} exceeds declared array length ${p.slots.length}`,
      origin,
    );
  }
  p.slots[seg.i] = child;
}

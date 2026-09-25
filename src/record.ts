import { PathError } from "./errors.ts";
import {
  PathInterner,
  comparePathNodes,
  pathToArray,
  type PathNode,
  type Seg,
} from "./path.ts";

export type Scalar = null | boolean | number | string;
export type JsonValue = Scalar | JsonValue[] | { [key: string]: JsonValue };

/**
 * One record per tree node. The encoding is explicit about every ambiguity:
 *  - "obj" / "arr" declare containers, so empty {} and [] exist as records;
 *  - "arr" carries the declared length `n`, so holes are exactly the indices
 *    < n that have no child record;
 *  - "val" carries JSON scalars, so null is a value, not an absence;
 *  - "del" is a tombstone used by patches to express "missing";
 *  - segments are tagged ("k" | "i"), so key "0" never means index 0.
 */
export type PathRecord =
  | { path: PathNode; op: "obj" }
  | { path: PathNode; op: "arr"; n: number }
  | { path: PathNode; op: "val"; v: Scalar }
  | { path: PathNode; op: "del" };

export function compareRecords(a: PathRecord, b: PathRecord): number {
  return comparePathNodes(a.path, b.path);
}

export function canonical(rec: PathRecord): string {
  switch (rec.op) {
    case "obj":
      return "obj";
    case "arr":
      return `arr:${rec.n}`;
    case "val":
      return `val:${JSON.stringify(rec.v)}`;
    case "del":
      return "del";
  }
}

type SegJson = ["k", string] | ["i", number];

function segToJson(seg: Seg): SegJson {
  return seg.t === "k" ? ["k", seg.k] : ["i", seg.i];
}

function segFromJson(raw: unknown, origin: string): Seg {
  if (
    Array.isArray(raw) &&
    raw.length === 2 &&
    raw[0] === "k" &&
    typeof raw[1] === "string"
  ) {
    return { t: "k", k: raw[1] };
  }
  if (
    Array.isArray(raw) &&
    raw.length === 2 &&
    raw[0] === "i" &&
    typeof raw[1] === "number" &&
    Number.isInteger(raw[1]) &&
    raw[1] >= 0
  ) {
    return { t: "i", i: raw[1] };
  }
  throw new PathError("MalformedRecord", [], `bad segment ${JSON.stringify(raw)}`, origin);
}

/** Wire format: one JSON object per line, path as tagged segment pairs. */
export function encodeRecord(rec: PathRecord): string {
  const p = pathToArray(rec.path).map(segToJson);
  switch (rec.op) {
    case "obj":
      return JSON.stringify({ p, op: "obj" });
    case "arr":
      return JSON.stringify({ p, op: "arr", n: rec.n });
    case "val":
      return JSON.stringify({ p, op: "val", v: rec.v });
    case "del":
      return JSON.stringify({ p, op: "del" });
  }
}

export function decodeRecord(
  line: string,
  interner: PathInterner,
  origin: string,
): PathRecord {
  let raw: unknown;
  try {
    raw = JSON.parse(line);
  } catch {
    throw new PathError("MalformedRecord", [], "invalid JSON line", origin);
  }
  if (typeof raw !== "object" || raw === null || !Array.isArray((raw as { p?: unknown }).p)) {
    throw new PathError("MalformedRecord", [], "record needs a path array `p`", origin);
  }
  const obj = raw as { p: unknown[]; op?: unknown; n?: unknown; v?: unknown };
  const segs = obj.p.map((s) => segFromJson(s, origin));
  const path = interner.internPath(segs);
  const fail = (msg: string): never => {
    throw new PathError("MalformedRecord", segs, msg, origin);
  };
  switch (obj.op) {
    case "obj":
      return { path, op: "obj" };
    case "arr":
      if (!Number.isInteger(obj.n) || (obj.n as number) < 0) fail("arr needs length n >= 0");
      return { path, op: "arr", n: obj.n as number };
    case "val": {
      const v = obj.v;
      if (v !== null && typeof v !== "boolean" && typeof v !== "number" && typeof v !== "string") {
        fail("val needs a JSON scalar v");
      }
      return { path, op: "val", v: v as Scalar };
    }
    case "del":
      return { path, op: "del" };
    default:
      return fail(`unknown op ${JSON.stringify(obj.op)}`);
  }
}

/**
 * Iterative encoder: JSON tree -> sorted record stream. Object keys are
 * emitted in sorted order (object key order is not meaningful); array
 * children in index order with holes skipped. Never recurses, so arbitrarily
 * deep trees are safe.
 */
export function* treeToRecords(
  root: JsonValue,
  interner: PathInterner,
  base?: PathNode,
): Generator<PathRecord> {
  const start = base ?? interner.root;
  const stack: { node: PathNode; value: JsonValue }[] = [{ node: start, value: root }];
  while (stack.length > 0) {
    const { node, value } = stack.pop() as { node: PathNode; value: JsonValue };
    if (Array.isArray(value)) {
      yield { path: node, op: "arr", n: value.length };
      for (let i = value.length - 1; i >= 0; i--) {
        if (i in value) {
          stack.push({ node: interner.intern(node, { t: "i", i }), value: value[i] as JsonValue });
        }
      }
    } else if (value !== null && typeof value === "object") {
      yield { path: node, op: "obj" };
      const keys = Object.keys(value).sort();
      for (let i = keys.length - 1; i >= 0; i--) {
        const key = keys[i];
        stack.push({
          node: interner.intern(node, { t: "k", k: key }),
          value: (value as { [k: string]: JsonValue })[key],
        });
      }
    } else if (
      value === null ||
      typeof value === "boolean" ||
      typeof value === "number" ||
      typeof value === "string"
    ) {
      yield { path: node, op: "val", v: value };
    } else {
      throw new PathError("MalformedRecord", pathToArray(node), "not a JSON-compatible value");
    }
  }
}

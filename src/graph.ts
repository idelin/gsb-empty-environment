/**
 * Cross-document $id/$ref object graph store.
 *
 * - Loads JSON-compatible trees from named documents; `{ $id: string }` gives
 *   an object a stable identity, `{ $ref: string }` is an explicit reference
 *   (also used for cycles/back-references).
 * - Identity comes only from `$id`; equal content never implies same identity.
 * - Local updates are copy-on-write: shared subgraphs are detached before
 *   mutation so other roots keep their version.
 * - publish() validates (no dangling refs, no type errors) and is atomic:
 *   on failure everything rolls back and old versions stay readable.
 * - Reverse references are maintained per version; GC only reclaims nodes
 *   unreachable from every retained version.
 * - Export is pruned per root and optionally inlines shared subgraphs
 *   (reversible: re-importing restores the same identities).
 */

export type Json =
  | null
  | boolean
  | number
  | string
  | Json[]
  | { [key: string]: Json };

export type PathSegment = string | number;
export type Path = PathSegment[];

export class GraphError extends Error {
  readonly path: Path;
  readonly source: string | undefined;

  constructor(message: string, path: Path = [], source?: string) {
    super(
      `${message} (at ${formatPath(path)}${
        source === undefined ? "" : ` in document "${source}"`
      })`,
    );
    this.name = new.target.name;
    this.path = path;
    this.source = source;
  }
}

export class DanglingRefError extends GraphError {
  readonly targets: readonly string[];
  constructor(message: string, path: Path, source: string | undefined, targets: readonly string[]) {
    super(message, path, source);
    this.targets = targets;
  }
}

export class TypeMismatchError extends GraphError {}
export class IdentityConflictError extends GraphError {}
export class UnknownRootError extends GraphError {}

export function formatPath(path: Path): string {
  let out = "$";
  for (const seg of path) {
    if (typeof seg === "number") {
      out += `[${seg}]`;
    } else if (/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(seg)) {
      out += `.${seg}`;
    } else {
      out += `[${JSON.stringify(seg)}]`;
    }
  }
  return out;
}

type NodeId = number;

type Primitive = null | boolean | number | string;

interface NodeValue {
  readonly kind: "node";
  readonly id: NodeId;
}

interface RefValue {
  readonly kind: "ref";
  readonly target: string;
}

type Value = Primitive | NodeValue | RefValue;

interface ObjectNode {
  readonly kind: "object";
  readonly id: NodeId;
  readonly sid: string | null;
  readonly entries: ReadonlyMap<string, Value>;
}

interface ArrayNode {
  readonly kind: "array";
  readonly id: NodeId;
  readonly items: readonly Value[];
}

type Node = ObjectNode | ArrayNode;

/** An immutable snapshot of the whole graph. */
export interface Version {
  readonly index: number;
  readonly nodes: ReadonlyMap<NodeId, Node>;
  readonly identities: ReadonlyMap<string, NodeId>;
  readonly roots: ReadonlyMap<string, NodeId>;
  /** child -> set of parents that structurally contain or reference it. */
  readonly reverseRefs: ReadonlyMap<NodeId, ReadonlySet<NodeId>>;
}

export interface PendingRef {
  readonly target: string;
  readonly path: Path;
  readonly source: string;
}

export type InspectResult =
  | { readonly type: "node"; readonly id: NodeId; readonly nodeKind: "object" | "array" }
  | { readonly type: "value"; readonly value: Primitive }
  | { readonly type: "pending"; readonly target: string };

interface Draft {
  nodes: Map<NodeId, Node>;
  identities: Map<string, NodeId>;
  roots: Map<string, NodeId>;
  dirty: boolean;
}

function isObjectJson(json: Json): json is { [key: string]: Json } {
  return typeof json === "object" && json !== null && !Array.isArray(json);
}

function isRefJson(json: Json): json is { $ref: string } {
  return (
    isObjectJson(json) &&
    typeof json.$ref === "string" &&
    Object.keys(json).length === 1
  );
}

function isIdJson(json: Json): json is { $id: string } {
  return isObjectJson(json) && typeof json.$id === "string";
}

function isNodeValue(v: Value): v is NodeValue {
  return typeof v === "object" && v !== null && (v as NodeValue).kind === "node";
}

function isRefValue(v: Value): v is RefValue {
  return typeof v === "object" && v !== null && (v as RefValue).kind === "ref";
}

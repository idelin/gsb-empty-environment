import type {
  EdgeDef,
  Graph,
  GraphDef,
  NodeDef,
  NodeKind,
} from "./types.ts";

const KINDS: ReadonlySet<string> = new Set([
  "source",
  "const",
  "assign",
  "alias",
  "sanitize",
  "boolSet",
  "sink",
  "branch",
  "nop",
]);

/** Required string / string-array / boolean fields per node kind. */
const REQUIRED: Record<NodeKind, Record<string, "string" | "string[]" | "boolean">> = {
  source: { var: "string", taints: "string[]" },
  const: { var: "string" },
  assign: { dst: "string", src: "string" },
  alias: { dst: "string", src: "string" },
  sanitize: { dst: "string", src: "string", types: "string[]" },
  boolSet: { var: "string", value: "boolean" },
  sink: { var: "string", accepts: "string[]" },
  branch: {},
  nop: {},
};

function fail(message: string): never {
  throw new Error(`invalid graph: ${message}`);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function checkNode(raw: unknown, index: number): NodeDef {
  if (!isRecord(raw)) fail(`nodes[${index}] must be an object`);
  const { id, kind } = raw;
  if (typeof id !== "string" || id.length === 0) fail(`nodes[${index}].id must be a non-empty string`);
  if (typeof kind !== "string" || !KINDS.has(kind)) {
    fail(`node "${id}" has unknown kind "${String(kind)}"`);
  }
  const required = REQUIRED[kind as NodeKind];
  for (const [field, type] of Object.entries(required)) {
    const value = raw[field];
    if (type === "string" && typeof value !== "string") {
      fail(`node "${id}" (${kind}) needs string field "${field}"`);
    }
    if (type === "boolean" && typeof value !== "boolean") {
      fail(`node "${id}" (${kind}) needs boolean field "${field}"`);
    }
    if (type === "string[]") {
      if (!Array.isArray(value) || value.some((v) => typeof v !== "string")) {
        fail(`node "${id}" (${kind}) needs string-array field "${field}"`);
      }
    }
  }
  if (raw.label !== undefined && typeof raw.label !== "string") {
    fail(`node "${id}" label must be a string`);
  }
  return raw as unknown as NodeDef;
}

function checkEdge(raw: unknown, index: number): EdgeDef {
  if (!isRecord(raw)) fail(`edges[${index}] must be an object`);
  const { from, to, cond } = raw;
  if (typeof from !== "string") fail(`edges[${index}].from must be a string`);
  if (typeof to !== "string") fail(`edges[${index}].to must be a string`);
  if (cond !== undefined) {
    if (!isRecord(cond)) fail(`edges[${index}].cond must be an object`);
    if (typeof cond.var !== "string" || typeof cond.value !== "boolean") {
      fail(`edges[${index}].cond needs { "var": string, "value": boolean }`);
    }
  }
  return raw as unknown as EdgeDef;
}

/** Validates raw JSON and builds an indexed graph. Throws on any inconsistency. */
export function parseGraph(raw: unknown): Graph {
  if (!isRecord(raw)) fail("top-level value must be an object");
  if (typeof raw.entry !== "string" || raw.entry.length === 0) {
    fail(`"entry" must be a non-empty node id`);
  }
  if (!Array.isArray(raw.nodes)) fail(`"nodes" must be an array`);
  if (!Array.isArray(raw.edges)) fail(`"edges" must be an array`);

  const nodes = new Map<string, NodeDef>();
  raw.nodes.forEach((rawNode, index) => {
    const node = checkNode(rawNode, index);
    if (nodes.has(node.id)) fail(`duplicate node id "${node.id}"`);
    nodes.set(node.id, node);
  });

  if (!nodes.has(raw.entry)) fail(`entry node "${raw.entry}" does not exist`);

  const edges = raw.edges.map(checkEdge);
  const out = new Map<string, EdgeDef[]>();
  for (const edge of edges) {
    if (!nodes.has(edge.from)) fail(`edge from unknown node "${edge.from}"`);
    if (!nodes.has(edge.to)) fail(`edge to unknown node "${edge.to}"`);
    const list = out.get(edge.from);
    if (list) list.push(edge);
    else out.set(edge.from, [edge]);
  }

  const def = raw as unknown as GraphDef;
  return {
    name: typeof def.name === "string" ? def.name : "<unnamed>",
    entry: raw.entry,
    nodes,
    out,
    edges,
  };
}

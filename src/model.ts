/**
 * Control-flow graph model and problem-defined taint rules.
 *
 * The rules (sources / sanitizers / sinks) are part of the problem input,
 * not hardcoded in the analyzer.
 */

export interface SourceRule {
  /** Rule id referenced by source nodes. */
  id: string;
  /** Type of the tainted value introduced by this source. */
  type: string;
}

export interface SanitizerRule {
  /** Rule id referenced by sanitize nodes. */
  id: string;
  /** Value types this sanitizer can clean. Ineffective on any other type. */
  types: string[];
}

export interface SinkRule {
  /** Rule id referenced by sink nodes (dangerous calls). */
  id: string;
  /** Types considered dangerous at this sink. Omit => any tainted arg is dangerous. */
  dangerTypes?: string[];
}

export interface Rules {
  sources: SourceRule[];
  sanitizers: SanitizerRule[];
  sinks: SinkRule[];
}

export type NodeOp =
  | "nop"
  | "source"
  | "assign"
  | "sanitize"
  | "alias"
  | "store"
  | "load"
  | "sink";

export interface CfgNode {
  id: string;
  op: NodeOp;
  /** source: rule id. */
  source?: string;
  /** assign/sanitize/alias/load: target variable. */
  var?: string;
  /** assign/store: variables read by the right-hand side expression. */
  expr?: string[];
  /** assign/load: optional declared type of the target variable. */
  type?: string;
  /** sanitize: rule id. */
  sanitizer?: string;
  /** alias: variable whose address is taken. */
  pointsTo?: string;
  /** alias: merged pointer may point to any of these variables. */
  pointsToAny?: string[];
  /** store/load: pointer variable. */
  pointer?: string;
  /** sink: rule id. */
  sink?: string;
  /** sink: argument variables of the dangerous call. */
  args?: string[];
}

export interface CfgEdge {
  from: string;
  to: string;
  /**
   * Boolean guard, finite domain: conjunction (`&&`) of clauses
   * `IDENT == LITERAL` / `IDENT != LITERAL` where LITERAL is
   * true | false | number | "string" | 'string'.
   */
  cond?: string;
}

export interface Graph {
  name: string;
  entry: string;
  rules: Rules;
  nodes: CfgNode[];
  edges: CfgEdge[];
  nodeById: Map<string, CfgNode>;
  outEdges: Map<string, CfgEdge[]>;
}

function fail(msg: string): never {
  throw new Error(`invalid CFG: ${msg}`);
}

function isObj(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function reqString(o: Record<string, unknown>, key: string, ctx: string): string {
  const v = o[key];
  if (typeof v !== "string" || v.length === 0) fail(`${ctx}: missing string field "${key}"`);
  return v as string;
}

function optString(o: Record<string, unknown>, key: string, ctx: string): string | undefined {
  const v = o[key];
  if (v === undefined) return undefined;
  if (typeof v !== "string" || v.length === 0) fail(`${ctx}: field "${key}" must be a non-empty string`);
  return v as string;
}

function reqStringArray(o: Record<string, unknown>, key: string, ctx: string): string[] {
  const v = o[key];
  if (!Array.isArray(v) || v.some((x) => typeof x !== "string")) {
    fail(`${ctx}: field "${key}" must be an array of strings`);
  }
  return v as string[];
}

const OPS: NodeOp[] = ["nop", "source", "assign", "sanitize", "alias", "store", "load", "sink"];

export function parseGraph(raw: unknown): Graph {
  if (!isObj(raw)) fail("top level must be an object");
  const name = optString(raw, "name", "graph") ?? "unnamed";
  const entry = reqString(raw, "entry", "graph");

  // ---- rules ----
  const rawRules = raw["rules"];
  if (!isObj(rawRules)) fail(`graph: missing object field "rules"`);
  const rules: Rules = { sources: [], sanitizers: [], sinks: [] };
  const ruleIds = { source: new Set<string>(), sanitizer: new Set<string>(), sink: new Set<string>() };

  const rawSources = rawRules["sources"] ?? [];
  const rawSanitizers = rawRules["sanitizers"] ?? [];
  const rawSinks = rawRules["sinks"] ?? [];
  if (!Array.isArray(rawSources) || !Array.isArray(rawSanitizers) || !Array.isArray(rawSinks)) {
    fail(`rules: "sources", "sanitizers" and "sinks" must be arrays`);
  }
  for (const s of rawSources) {
    if (!isObj(s)) fail(`rules.sources: entries must be objects`);
    const id = reqString(s, "id", "rules.sources");
    const type = reqString(s, "type", `rules.sources[${id}]`);
    if (ruleIds.source.has(id)) fail(`rules.sources: duplicate id "${id}"`);
    ruleIds.source.add(id);
    rules.sources.push({ id, type });
  }
  for (const s of rawSanitizers) {
    if (!isObj(s)) fail(`rules.sanitizers: entries must be objects`);
    const id = reqString(s, "id", "rules.sanitizers");
    const types = reqStringArray(s, "types", `rules.sanitizers[${id}]`);
    if (ruleIds.sanitizer.has(id)) fail(`rules.sanitizers: duplicate id "${id}"`);
    ruleIds.sanitizer.add(id);
    rules.sanitizers.push({ id, types });
  }
  for (const s of rawSinks) {
    if (!isObj(s)) fail(`rules.sinks: entries must be objects`);
    const id = reqString(s, "id", "rules.sinks");
    let dangerTypes: string[] | undefined;
    if (s["dangerTypes"] !== undefined) dangerTypes = reqStringArray(s, "dangerTypes", `rules.sinks[${id}]`);
    if (ruleIds.sink.has(id)) fail(`rules.sinks: duplicate id "${id}"`);
    ruleIds.sink.add(id);
    rules.sinks.push({ id, dangerTypes });
  }

  // ---- nodes ----
  const rawNodes = raw["nodes"];
  if (!Array.isArray(rawNodes) || rawNodes.length === 0) fail(`graph: "nodes" must be a non-empty array`);
  const nodes: CfgNode[] = [];
  const nodeById = new Map<string, CfgNode>();
  for (const n of rawNodes) {
    if (!isObj(n)) fail(`nodes: entries must be objects`);
    const id = reqString(n, "id", "nodes");
    const ctx = `nodes[${id}]`;
    const op = reqString(n, "op", ctx) as NodeOp;
    if (!OPS.includes(op)) fail(`${ctx}: unknown op "${op}"`);
    const node: CfgNode = { id, op };
    switch (op) {
      case "source": {
        node.source = reqString(n, "source", ctx);
        if (!ruleIds.source.has(node.source)) fail(`${ctx}: unknown source rule "${node.source}"`);
        node.var = reqString(n, "var", ctx);
        break;
      }
      case "assign": {
        node.var = reqString(n, "var", ctx);
        node.expr = reqStringArray(n, "expr", ctx);
        node.type = optString(n, "type", ctx);
        break;
      }
      case "sanitize": {
        node.sanitizer = reqString(n, "sanitizer", ctx);
        if (!ruleIds.sanitizer.has(node.sanitizer)) fail(`${ctx}: unknown sanitizer rule "${node.sanitizer}"`);
        node.var = reqString(n, "var", ctx);
        break;
      }
      case "alias": {
        node.var = reqString(n, "var", ctx);
        const single = optString(n, "pointsTo", ctx);
        const many = n["pointsToAny"] !== undefined ? reqStringArray(n, "pointsToAny", ctx) : undefined;
        if ((single === undefined) === (many === undefined)) {
          fail(`${ctx}: exactly one of "pointsTo" / "pointsToAny" is required`);
        }
        if (single !== undefined) node.pointsTo = single;
        if (many !== undefined) {
          if (many.length === 0) fail(`${ctx}: "pointsToAny" must not be empty`);
          node.pointsToAny = many;
        }
        break;
      }
      case "store": {
        node.pointer = reqString(n, "pointer", ctx);
        node.expr = reqStringArray(n, "expr", ctx);
        break;
      }
      case "load": {
        node.var = reqString(n, "var", ctx);
        node.pointer = reqString(n, "pointer", ctx);
        node.type = optString(n, "type", ctx);
        break;
      }
      case "sink": {
        node.sink = reqString(n, "sink", ctx);
        if (!ruleIds.sink.has(node.sink)) fail(`${ctx}: unknown sink rule "${node.sink}"`);
        node.args = reqStringArray(n, "args", ctx);
        break;
      }
      case "nop":
        break;
    }
    if (nodeById.has(id)) fail(`nodes: duplicate id "${id}"`);
    nodeById.set(id, node);
    nodes.push(node);
  }
  if (!nodeById.has(entry)) fail(`graph: entry node "${entry}" does not exist`);

  // ---- edges ----
  const rawEdges = raw["edges"];
  if (!Array.isArray(rawEdges)) fail(`graph: "edges" must be an array`);
  const edges: CfgEdge[] = [];
  const outEdges = new Map<string, CfgEdge[]>();
  for (const e of rawEdges) {
    if (!isObj(e)) fail(`edges: entries must be objects`);
    const from = reqString(e, "from", "edges");
    const to = reqString(e, "to", "edges");
    if (!nodeById.has(from)) fail(`edges: unknown from-node "${from}"`);
    if (!nodeById.has(to)) fail(`edges: unknown to-node "${to}"`);
    const cond = optString(e, "cond", `edges[${from}->${to}]`);
    const edge: CfgEdge = cond === undefined ? { from, to } : { from, to, cond };
    edges.push(edge);
    const list = outEdges.get(from) ?? [];
    list.push(edge);
    outEdges.set(from, list);
  }

  return { name, entry, rules, nodes, edges, nodeById, outEdges };
}

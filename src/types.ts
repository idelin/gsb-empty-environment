/**
 * Schema of the control-flow graph (CFG) consumed by the analyzer.
 *
 * A graph is a set of statement nodes connected by directed edges.
 * Edges may carry a boolean condition (`cond`): the edge may only be
 * traversed when the condition variable currently holds the required
 * value (or is unconstrained, in which case traversing the edge
 * constrains it).
 */

export type TaintType = string;

/** Condition guarding an edge: `var` must equal `value` for the edge to be feasible. */
export interface EdgeCondition {
  var: string;
  value: boolean;
}

interface NodeBase {
  id: string;
  /** Optional human-readable name, e.g. the dangerous API of a sink. */
  label?: string;
}

/** Introduces tainted data: `var` becomes tainted with all of `taints`. */
export interface SourceNode extends NodeBase {
  kind: "source";
  var: string;
  taints: TaintType[];
}

/** Assigns a clean constant: clears every taint of `var`. */
export interface ConstNode extends NodeBase {
  kind: "const";
  var: string;
}

/** Copies the value (and taint) of `src` into `dst`. */
export interface AssignNode extends NodeBase {
  kind: "assign";
  dst: string;
  src: string;
}

/**
 * Makes `dst` an alias (reference) of `src`: `dst := &src`.
 * Later writes through `dst` also overwrite `src`, which can
 * invalidate a previous sanitization of `src`.
 */
export interface AliasNode extends NodeBase {
  kind: "alias";
  dst: string;
  src: string;
}

/**
 * Sanitizes `src` into `dst`, removing ONLY the listed taint `types`.
 * Any taint type not listed survives the sanitizer.
 */
export interface SanitizeNode extends NodeBase {
  kind: "sanitize";
  dst: string;
  src: string;
  types: TaintType[];
}

/** Sets a boolean condition variable to a constant. */
export interface BoolSetNode extends NodeBase {
  kind: "boolSet";
  var: string;
  value: boolean;
}
/** Dangerous call. Fires when the taint of `var` intersects `accepts`. */
export interface SinkNode extends NodeBase {
  kind: "sink";
  var: string;
  accepts: TaintType[];
}

/** Pure control-flow split; conditions live on the outgoing edges. */
export interface BranchNode extends NodeBase {
  kind: "branch";
}

export interface NopNode extends NodeBase {
  kind: "nop";
}

export type NodeDef =
  | SourceNode
  | ConstNode
  | AssignNode
  | AliasNode
  | SanitizeNode
  | BoolSetNode
  | SinkNode
  | BranchNode
  | NopNode;

export type NodeKind = NodeDef["kind"];

export interface EdgeDef {
  from: string;
  to: string;
  cond?: EdgeCondition;
}

export interface GraphDef {
  name?: string;
  entry: string;
  nodes: NodeDef[];
  edges: EdgeDef[];
}

/** Validated, index-ready graph. */
export interface Graph {
  name: string;
  entry: string;
  nodes: Map<string, NodeDef>;
  /** Outgoing edges per node id. */
  out: Map<string, EdgeDef[]>;
  edges: EdgeDef[];
}

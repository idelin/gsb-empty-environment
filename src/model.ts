import type { Literal } from './formula.ts';

/**
 * Finite control-flow models.
 *
 * A model is a rooted DAG (no recursion in the abstract machine; mutual
 * recursion lives in the package's module graph). Every node carries at most
 * two successors. The same node shape serves both:
 *
 *   - declarations: nodes describe *obligations* attached to exits;
 *   - implementations: nodes describe *effects* the machine performs.
 */
export type NodeId = string;
export type ReturnTag = string;

interface NodeBase {
  readonly id: NodeId;
}

/** Unique entry; carries the input constraint (precondition). */
export interface EntryNode extends NodeBase {
  readonly kind: 'entry';
  readonly input: readonly Literal[];
  readonly next: NodeId;
}

/** Extend the path condition with a fact. */
export interface AssumeNode extends NodeBase {
  readonly kind: 'assume';
  readonly literals: readonly Literal[];
  readonly next: NodeId;
}

/** Establish facts as a side effect of executing the program. */
export interface EffectNode extends NodeBase {
  readonly kind: 'effect';
  readonly establishes: readonly Literal[];
  readonly next: NodeId;
}

/**
 * Retract facts. This is the "failure annotation" transition: after an
 * exceptional event the object may have lost previously advertised
 * properties, so their annotations are removed instead of propagated.
 */
export interface InvalidateNode extends NodeBase {
  readonly kind: 'invalidate';
  readonly invalidates: readonly Literal[];
  readonly next: NodeId;
}

/** A guarded two-way split; `elseNext` may equal `thenNext`. */
export interface BranchNode extends NodeBase {
  readonly kind: 'branch';
  readonly condition: Literal;
  readonly thenNext: NodeId;
  readonly elseNext: NodeId;
}

/** Normal exit, labelled so callers can distinguish return shapes. */
export interface ReturnNode extends NodeBase {
  readonly kind: 'return';
  readonly tag: ReturnTag;
  /** Declaration-side obligation; unused on implementation nodes. */
  readonly requires: readonly Literal[];
}

/** Exceptional exit with the object state observed by the catch site. */
export interface ThrowNode extends NodeBase {
  readonly kind: 'throw';
  readonly label: string;
  /** Declaration-side obligation on post-exception state. */
  readonly requires: readonly Literal[];
}

export type FlowNode =
  | EntryNode
  | AssumeNode
  | EffectNode
  | InvalidateNode
  | BranchNode
  | ReturnNode
  | ThrowNode;

export interface FlowModel {
  readonly name: string;
  readonly entry: NodeId;
  readonly nodes: Readonly<Record<NodeId, FlowNode>>;
}

export function successors(node: FlowNode): NodeId[] {
  switch (node.kind) {
    case 'branch':
      return node.thenNext === node.elseNext ? [node.thenNext] : [node.thenNext, node.elseNext];
    case 'return':
    case 'throw':
      return [];
    default:
      return [node.next];
  }
}

export function getNode(model: FlowModel, id: NodeId): FlowNode {
  const node = model.nodes[id];
  if (node === undefined) {
    throw new Error(`dangling control-flow edge: unknown node "${id}" in ${model.name}`);
  }
  return node;
}

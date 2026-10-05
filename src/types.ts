export type NodeId = number;
export type EdgeId = number;
export type Label = string;
export type AutomatonStateId = number;

export interface Edge {
  id: EdgeId;
  from: NodeId;
  to: NodeId;
  label: Label;
}

export interface GraphSpec {
  nodeCount: number;
  edges: Edge[];
}

export interface AutomatonTransition {
  from: AutomatonStateId;
  label: Label;
  to: AutomatonStateId;
}

export interface AutomatonSpec {
  stateCount: number;
  initialStates: AutomatonStateId[];
  acceptingStates: AutomatonStateId[];
  transitions: AutomatonTransition[];
}

/** A state of the search is always a (graph node, automaton state) pair. */
export interface ProductState {
  node: NodeId;
  automatonState: AutomatonStateId;
}

export function productStateKey(state: ProductState): string {
  return state.node + "|" + state.automatonState;
}

export function compareEdgeIdSequences(a: readonly EdgeId[], b: readonly EdgeId[]): number {
  if (a.length !== b.length) return a.length - b.length;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return a[i] - b[i];
  }
  return 0;
}

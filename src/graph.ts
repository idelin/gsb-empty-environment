import type {
  ActionName,
  GraphSpec,
  GraphVersion,
  StateName,
  Transition,
  WorkflowGraph,
} from "./types";
import { UnknownGraphVersionError, WorkflowError } from "./errors";

export function defineGraph(spec: GraphSpec): WorkflowGraph {
  const states = new Set(spec.states);
  if (!states.has(spec.initialState)) {
    throw new WorkflowError(
      `Initial state "${spec.initialState}" is not declared in states.`,
    );
  }
  const terminalStates = new Set(spec.terminalStates ?? []);
  for (const terminal of terminalStates) {
    if (!states.has(terminal)) {
      throw new WorkflowError(
        `Terminal state "${terminal}" is not declared in states.`,
      );
    }
  }
  for (const transition of spec.transitions) {
    if (!states.has(transition.from) || !states.has(transition.to)) {
      throw new WorkflowError(
        `Transition ${transition.from} --${transition.action}--> ${transition.to} ` +
          `references an undeclared state.`,
      );
    }
  }
  return Object.freeze({
    name: spec.name,
    version: spec.version,
    initialState: spec.initialState,
    states,
    transitions: Object.freeze([...spec.transitions]),
    terminalStates,
  });
}

export function availableActions(
  graph: WorkflowGraph,
  state: StateName,
): ActionName[] {
  return graph.transitions
    .filter((transition) => transition.from === state)
    .map((transition) => transition.action);
}

export function findTransition(
  graph: WorkflowGraph,
  state: StateName,
  action: ActionName,
): Transition | undefined {
  return graph.transitions.find(
    (transition) => transition.from === state && transition.action === action,
  );
}

export class GraphRegistry {
  private readonly graphs = new Map<GraphVersion, WorkflowGraph>();

  register(graph: WorkflowGraph): this {
    if (this.graphs.has(graph.version)) {
      throw new WorkflowError(
        `A graph is already registered for version "${graph.version}".`,
      );
    }
    this.graphs.set(graph.version, graph);
    return this;
  }

  get(version: GraphVersion): WorkflowGraph {
    const graph = this.graphs.get(version);
    if (!graph) {
      throw new UnknownGraphVersionError(version);
    }
    return graph;
  }

  has(version: GraphVersion): boolean {
    return this.graphs.has(version);
  }
}

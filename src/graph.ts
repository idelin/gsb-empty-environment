import { InvalidGraphError } from "./errors.ts";
import type { GraphVersion } from "./types.ts";

export function validateGraph(graph: GraphVersion, previous?: GraphVersion): void {
  if (!Number.isInteger(graph.version) || graph.version < 1) {
    throw new InvalidGraphError(`Graph version must be a positive integer, got ${graph.version}.`);
  }
  if (graph.states.length === 0) {
    throw new InvalidGraphError(`Graph v${graph.version} must declare at least one state.`);
  }
  if (new Set(graph.states).size !== graph.states.length) {
    throw new InvalidGraphError(`Graph v${graph.version} declares duplicate states.`);
  }
  if (!graph.states.includes(graph.initialState)) {
    throw new InvalidGraphError(
      `Initial state "${graph.initialState}" is not a state of graph v${graph.version}.`,
    );
  }
  for (const transition of graph.transitions) {
    if (transition.event.length === 0) {
      throw new InvalidGraphError(`Graph v${graph.version} has a transition with an empty event.`);
    }
    if (!graph.states.includes(transition.to)) {
      throw new InvalidGraphError(
        `Transition "${transition.event}" targets unknown state "${transition.to}" in graph v${graph.version}.`,
      );
    }
    for (const from of transition.from) {
      if (!graph.states.includes(from)) {
        throw new InvalidGraphError(
          `Transition "${transition.event}" sources unknown state "${from}" in graph v${graph.version}.`,
        );
      }
    }
  }
  if (previous === undefined) {
    if (graph.stateMapping !== undefined) {
      throw new InvalidGraphError(
        `Graph v${graph.version} is the first registered version and must not declare a stateMapping.`,
      );
    }
    return;
  }
  const mapping = graph.stateMapping;
  if (mapping === undefined) {
    throw new InvalidGraphError(
      `Graph v${graph.version} must declare a stateMapping from v${previous.version}.`,
    );
  }
  for (const [from, to] of Object.entries(mapping)) {
    if (!previous.states.includes(from)) {
      throw new InvalidGraphError(
        `stateMapping of graph v${graph.version} references unknown v${previous.version} state "${from}".`,
      );
    }
    if (!graph.states.includes(to)) {
      throw new InvalidGraphError(
        `stateMapping of graph v${graph.version} maps "${from}" to unknown state "${to}".`,
      );
    }
  }
}

export function actionsFrom(graph: GraphVersion, state: string): string[] {
  return graph.transitions.filter((t) => t.from.includes(state)).map((t) => t.event);
}

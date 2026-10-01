import { breakPositions } from './nodes.ts';
import {
  computeFrontier,
  finalize,
  stateKey,
  type BreakResult,
  type DPState,
  type FrontierResult,
  type StateMap,
} from './breaker.ts';
import type { BreakOptions, Node } from './types.ts';

export interface IncrementalSession {
  nodes: Node[];
  options: BreakOptions;
  positions: number[];
  frontier: StateMap[];
  result: BreakResult;
}

function initialStateMap(): StateMap {
  const s: DPState = {
    cost: 0,
    hyphens: 0,
    prevHyphenated: false,
    prevWidth: null,
    path: [],
  };
  return new Map([[stateKey(s), s]]);
}

export function createSession(nodes: Node[], options: BreakOptions): IncrementalSession {
  const positions = breakPositions(nodes);
  const fr = computeFrontier(nodes, options, positions, 0, initialStateMap());
  const result = finalize(nodes, options, fr);
  return { nodes, options, positions, frontier: fr.frontier, result };
}

function nodesEqual(a: Node, b: Node): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

export interface EditReport {
  result: BreakResult;
  reuse: {
    /** break positions whose DP frontier states were carried over unchanged */
    reusedUpToPosition: number;
    reusedStates: number;
    recomputedStates: number;
    relaxations: number;
  };
}

/**
 * Incremental edit: replace nodes[start .. end) with `replacement`.
 * Frontier states at break positions <= the common prefix depend only on
 * content before them, so they are reused verbatim; the DP resumes from the
 * last safe break position. The result is identical to a full recompute.
 */
export function applyEdit(
  session: IncrementalSession,
  start: number,
  end: number,
  replacement: Node[],
): EditReport {
  const newNodes = [...session.nodes.slice(0, start), ...replacement, ...session.nodes.slice(end)];

  let prefix = 0;
  while (
    prefix < session.nodes.length &&
    prefix < newNodes.length &&
    nodesEqual(session.nodes[prefix], newNodes[prefix])
  ) {
    prefix++;
  }

  const positions = breakPositions(newNodes);
  // Largest break-position index whose states only saw unchanged content.
  let safeIndex = 0;
  while (safeIndex < positions.length && positions[safeIndex] <= prefix) safeIndex++;
  safeIndex--; // positions[safeIndex] <= prefix < positions[safeIndex+1]

  const reusedStates = session.frontier
    .slice(0, safeIndex + 1)
    .reduce((n, m) => n + (m ? m.size : 0), 0);

  const fr: FrontierResult = computeFrontier(
    newNodes,
    session.options,
    positions,
    safeIndex,
    session.frontier[safeIndex],
    session.frontier,
    0, // lines starting before the edit may cross into edited content
  );
  const result = finalize(newNodes, session.options, fr);
  const totalStates = fr.frontier.reduce((n, m) => n + (m ? m.size : 0), 0);

  session.nodes = newNodes;
  session.positions = positions;
  session.frontier = fr.frontier;
  session.result = result;

  return {
    result,
    reuse: {
      reusedUpToPosition: positions[safeIndex],
      reusedStates,
      recomputedStates: totalStates - reusedStates,
      relaxations: fr.relaxations,
    },
  };
}

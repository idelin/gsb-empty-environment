import type {
  EdgeCondition,
  Graph,
  NodeDef,
  TaintType,
} from "./types.ts";

/** One step of a reported path. */
export interface PathStep {
  nodeId: string;
  kind: NodeDef["kind"];
  description: string;
  /** Boolean condition of the edge that led into this step, if any. */
  viaCondition?: EdgeCondition;
}

export interface AnalysisResult {
  /** True when a feasible path delivers accepted taint to a sink. */
  found: boolean;
  sinkId?: string;
  sinkLabel?: string;
  /** Taint types accepted by the sink that actually arrive there. */
  taintAtSink?: TaintType[];
  /** Boolean constraints collected along the reported path. */
  constraints?: Record<string, boolean>;
  /** Shortest feasible path, trimmed to start at the originating source. */
  path?: PathStep[];
  /** Number of explored (node, env, bools, aliases) states. */
  exploredStates: number;
  /** True when the search hit the state budget before exhausting. */
  truncated?: boolean;
}

export interface AnalyzeOptions {
  /** Safety budget for the state-space exploration. */
  maxStates?: number;
}

type Env = Map<string, Set<TaintType>>;
type Aliases = Map<string, Set<string>>;
type Bools = Map<string, boolean>;

interface State {
  nodeId: string;
  env: Env;
  aliases: Aliases;
  bools: Bools;
  /** Index of the predecessor state in `states`, -1 for the root. */
  parent: number;
  viaCondition?: EdgeCondition;
}

const DEFAULT_MAX_STATES = 200_000;

function cloneEnv(env: Env): Env {
  const copy: Env = new Map();
  for (const [key, value] of env) copy.set(key, new Set(value));
  return copy;
}

function cloneAliases(aliases: Aliases): Aliases {
  const copy: Aliases = new Map();
  for (const [key, value] of aliases) copy.set(key, new Set(value));
  return copy;
}

/**
 * Follows the points-to chain of `name` to the concrete variable(s) it
 * ultimately refers to. A non-alias variable refers to itself.
 */
function resolveRoots(aliases: Aliases, name: string): Set<string> {
  const roots = new Set<string>();
  const worklist = [name];
  const seen = new Set<string>();
  while (worklist.length > 0) {
    const current = worklist.pop() as string;
    if (seen.has(current)) continue;
    seen.add(current);
    const targets = aliases.get(current);
    if (targets === undefined) {
      roots.add(current);
    } else {
      for (const target of targets) worklist.push(target);
    }
  }
  return roots;
}

/** Taint of a value read through (possibly) an alias. */
function readTaint(env: Env, aliases: Aliases, name: string): Set<TaintType> {
  const result = new Set<TaintType>();
  for (const root of resolveRoots(aliases, name)) {
    for (const type of env.get(root) ?? []) result.add(type);
  }
  return result;
}

/**
 * Writes `taint` into `name`. If `name` is an alias, the write goes
 * through to every aliased variable — this is what lets an alias write
 * re-taint (invalidate) a previously sanitized value.
 */
function writeTaint(env: Env, aliases: Aliases, name: string, taint: ReadonlySet<TaintType>): void {
  for (const root of resolveRoots(aliases, name)) {
    env.set(root, new Set(taint));
  }
}

/** Applies the data-flow effect of a node onto a copied environment. */
function applyNode(node: NodeDef, env: Env, aliases: Aliases, bools: Bools): void {
  switch (node.kind) {
    case "source":
      writeTaint(env, aliases, node.var, new Set(node.taints));
      break;
    case "const":
      writeTaint(env, aliases, node.var, new Set());
      break;
    case "assign":
      writeTaint(env, aliases, node.dst, readTaint(env, aliases, node.src));
      break;
    case "alias":
      aliases.set(node.dst, resolveRoots(aliases, node.src));
      break;
    case "sanitize": {
      // Sanitization is type-directed: only the listed types are removed.
      const cleaned = readTaint(env, aliases, node.src);
      for (const type of node.types) cleaned.delete(type);
      writeTaint(env, aliases, node.dst, cleaned);
      break;
    }
    case "boolSet":
      bools.set(node.var, node.value);
      break;
    case "sink":
    case "branch":
    case "nop":
      break;
  }
}

function sortedKey(entries: Iterable<readonly [string, string]>): string {
  return [...entries]
    .map(([key, value]) => `${key}=${value}`)
    .sort()
    .join(";");
}

function boolsKey(bools: Bools): string {
  return sortedKey([...bools].map(([key, value]) => [key, String(value)] as const));
}

function aliasesKey(aliases: Aliases): string {
  const flat: Array<readonly [string, string]> = [];
  for (const [name, targets] of aliases) {
    flat.push([name, [...targets].sort().join("|")]);
  }
  return sortedKey(flat);
}

/** True when `sup` covers every taint of `sub`, variable by variable. */
function envCovers(sup: Env, sub: Env): boolean {
  for (const [name, types] of sub) {
    const supTypes = sup.get(name);
    for (const type of types) {
      if (supTypes === undefined || !supTypes.has(type)) return false;
    }
  }
  return true;
}

export function describeNode(node: NodeDef): string {
  switch (node.kind) {
    case "source":
      return `${node.var} := <source: ${node.taints.join(", ")}>`;
    case "const":
      return `${node.var} := <const>`;
    case "assign":
      return `${node.dst} := ${node.src}`;
    case "alias":
      return `${node.dst} := &${node.src}`;
    case "sanitize":
      return `${node.dst} := sanitize(${node.src}, [${node.types.join(", ")}])`;
    case "boolSet":
      return `${node.var} := ${node.value}`;
    case "sink":
      return `dangerous call ${node.label ?? node.id}(${node.var}) accepts [${node.accepts.join(", ")}]`;
    case "branch":
      return `<branch>`;
    case "nop":
      return `<nop>`;
  }
}

/**
 * Path-sensitive BFS for the shortest feasible source-to-sink taint flow.
 *
 * Feasibility: edge conditions constrain boolean variables; an edge whose
 * condition contradicts an already-collected constraint is never taken,
 * so mutually contradictory branches are excluded from any reported path.
 *
 * Merge rule: taint environments from different predecessors coexist as
 * separate path-sensitive states; a state is pruned only when a previously
 * seen state at the same node, with identical boolean constraints and
 * alias structure, already holds a superset of its taint (the transfer
 * functions are monotone, so the pruned state cannot reach anything the
 * dominating state cannot). BFS order then guarantees the first tainted
 * sink found lies on a shortest feasible path.
 */
export function analyze(graph: Graph, options: AnalyzeOptions = {}): AnalysisResult {
  const maxStates = options.maxStates ?? DEFAULT_MAX_STATES;

  const states: State[] = [];
  const queue: number[] = [];
  let head = 0;
  /** Dominance index: (node, bools, aliases) -> already-seen taint envs. */
  const seen = new Map<string, Env[]>();

  const tryPush = (state: State): boolean => {
    const key = `${state.nodeId}${boolsKey(state.bools)}${aliasesKey(state.aliases)}`;
    let envs = seen.get(key);
    if (envs === undefined) {
      envs = [];
      seen.set(key, envs);
    }
    for (const env of envs) {
      if (envCovers(env, state.env)) return false;
    }
    envs.push(cloneEnv(state.env));
    states.push(state);
    queue.push(states.length - 1);
    return true;
  };

  tryPush({
    nodeId: graph.entry,
    env: new Map(),
    aliases: new Map(),
    bools: new Map(),
    parent: -1,
  });

  let truncated = false;

  while (head < queue.length) {
    if (states.length > maxStates) {
      truncated = true;
      break;
    }
    const index = queue[head] as number;
    head += 1;
    const state = states[index] as State;
    const node = graph.nodes.get(state.nodeId) as NodeDef;

    applyNode(node, state.env, state.aliases, state.bools);

    if (node.kind === "sink") {
      const arriving = readTaint(state.env, state.aliases, node.var);
      const hit = node.accepts.filter((type) => arriving.has(type));
      if (hit.length > 0) {
        return buildResult(graph, states, index, node, hit);
      }
    }

    for (const edge of graph.out.get(node.id) ?? []) {
      const bools: Bools = new Map(state.bools);
      if (edge.cond !== undefined) {
        const current = bools.get(edge.cond.var);
        // Contradictory constraint: this branch is infeasible on this path.
        if (current !== undefined && current !== edge.cond.value) continue;
        bools.set(edge.cond.var, edge.cond.value);
      }
      const child: State = {
        nodeId: edge.to,
        env: cloneEnv(state.env),
        aliases: cloneAliases(state.aliases),
        bools,
        parent: index,
      };
      if (edge.cond !== undefined) child.viaCondition = edge.cond;
      tryPush(child);
    }
  }

  return { found: false, exploredStates: states.length, truncated };
}

function buildResult(
  graph: Graph,
  states: State[],
  sinkIndex: number,
  sink: Extract<NodeDef, { kind: "sink" }>,
  taintAtSink: TaintType[],
): AnalysisResult {
  const chain: State[] = [];
  for (let i: number = sinkIndex; i !== -1; i = (states[i] as State).parent) {
    chain.push(states[i] as State);
  }
  chain.reverse();

  const steps: PathStep[] = chain.map((state) => {
    const node = graph.nodes.get(state.nodeId) as NodeDef;
    const step: PathStep = {
      nodeId: node.id,
      kind: node.kind,
      description: describeNode(node),
    };
    if (state.viaCondition !== undefined) step.viaCondition = state.viaCondition;
    return step;
  });

  // Trim the prefix before the originating source so the reported path
  // runs source -> sink, as requested.
  const sourceAt = steps.findIndex((step) => step.kind === "source");
  const path = sourceAt > 0 ? steps.slice(sourceAt) : steps;

  const constraints: Record<string, boolean> = {};
  for (const [name, value] of (states[sinkIndex] as State).bools) {
    constraints[name] = value;
  }

  return {
    found: true,
    sinkId: sink.id,
    sinkLabel: sink.label,
    taintAtSink,
    constraints,
    path,
    exploredStates: states.length,
  };
}

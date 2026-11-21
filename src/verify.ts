import {
  type Literal,
  intersectLiterals,
  not,
  unsatisfied,
} from './formula.ts';
import {
  type FlowModel,
  type FlowNode,
  type ReturnTag,
  getNode,
} from './model.ts';
import { enumeratePaths, type FlowPath } from './paths.ts';
import { AbstractState } from './state.ts';
import { VERIFIED_BRAND, type VerifiedSummary } from './summary.ts';

/**
 * Contract verification, path by path.
 *
 * The verifier interprets the implementation's finite control-flow model,
 * threads an abstract state along every structural path, and checks each
 * exit against the declaration. A summary is minted only when *every* path
 * satisfies *every* obligation, so failure annotations can never reach a
 * caller as if they still held.
 */

export interface CompiledContract {
  readonly name: string;
  readonly precondition: readonly Literal[];
  readonly returns: Readonly<Record<ReturnTag, readonly Literal[]>>;
  readonly throws: Readonly<Record<string, readonly Literal[]>>;
}

/** Compile a declaration model into its minimal logical contract. */
export function compileDeclaration(declaration: FlowModel): CompiledContract {
  const entry = getNode(declaration, declaration.entry);
  if (entry.kind !== 'entry') {
    throw new Error(`declaration ${declaration.name} must start at an entry node`);
  }

  const precondition = [...entry.input];
  const returns = new Map<ReturnTag, Literal[]>();
  const throws = new Map<string, Literal[]>();

  for (const node of Object.values(declaration.nodes)) {
    if (node.kind === 'return') {
      const existing = returns.get(node.tag);
      returns.set(
        node.tag,
        existing === undefined ? [...node.requires] : intersectLiterals([existing, node.requires]),
      );
    } else if (node.kind === 'throw') {
      const existing = throws.get(node.label);
      throws.set(
        node.label,
        existing === undefined ? [...node.requires] : intersectLiterals([existing, node.requires]),
      );
    }
  }

  return {
    name: declaration.name,
    precondition,
    returns: Object.fromEntries(returns),
    throws: Object.fromEntries(throws),
  };
}

export interface PathFailure {
  readonly pathIndex: number;
  readonly path: readonly string[];
  readonly exit?: FlowNode;
  readonly missing: readonly Literal[];
  readonly reason: 'return-unsatisfied' | 'throw-unsatisfied' | 'declared-exit-unreachable';
}

export interface VerificationSuccess {
  readonly ok: true;
  readonly pathCount: number;
  readonly feasiblePathCount: number;
  readonly summary: VerifiedSummary;
}

export interface VerificationFailure {
  readonly ok: false;
  readonly pathCount: number;
  readonly failures: readonly PathFailure[];
  readonly summary: null;
}

export type VerificationResult = VerificationSuccess | VerificationFailure;

interface SimulatedPath {
  readonly pathIndex: number;
  readonly path: FlowPath;
  readonly exit: Extract<FlowNode, { kind: 'return' | 'throw' }>;
  readonly state: AbstractState;
}

function nodeIds(path: FlowPath): string[] {
  return path.nodes.map((node) => node.id);
}

/**
 * Interpret one structural path.
 *
 * Returns `null` when the path is infeasible (an assumption contradicts an
 * established fact). Infeasible paths are excluded from both satisfying
 * evidence and counterexamples.
 */
function simulate(
  pathIndex: number,
  path: FlowPath,
  contract: CompiledContract,
): SimulatedPath | null {
  let state = AbstractState.empty();
  const initial = state.assumeAll(contract.precondition);
  if (initial === null) {
    throw new Error(`contract ${contract.name} has an inconsistent precondition`);
  }
  state = initial;

  for (let index = 0; index < path.nodes.length; index += 1) {
    const node = path.nodes[index];

    switch (node.kind) {
      case 'entry': {
        // Implementation-side inputs are ignored: the declaration owns the
        // precondition, which has already been installed above.
        break;
      }
      case 'assume': {
        const next = state.assumeAll(node.literals);
        if (next === null) {
          return null;
        }
        state = next;
        break;
      }
      case 'effect': {
        for (const literal of node.establishes) {
          state = state.effect(literal);
        }
        break;
      }
      case 'invalidate': {
        // Failed annotations are removed here; nothing downstream can
        // observe them through the surviving state.
        state = state.invalidate(node.invalidates);
        break;
      }
      case 'branch': {
        const branch = node as Extract<FlowNode, { kind: 'branch' }>;
        const takeThen = path.nodes[index + 1]?.id === branch.thenNext;
        const assumed = takeThen ? branch.condition : not(branch.condition);
        const next = state.assume(assumed);
        if (next === null) {
          return null;
        }
        state = next;
        break;
      }
      case 'return':
      case 'throw': {
        return { pathIndex, path, exit: node, state };
      }
    }
  }

  throw new Error(`path in ${contract.name} ends without a return or throw node`);
}

function summarizeExitStates(simulated: readonly SimulatedPath[]): {
  returns: Record<ReturnTag, Literal[]>;
  throws: Record<string, Literal[]>;
} {
  const returnGroups = new Map<ReturnTag, Literal[][]>();
  const throwGroups = new Map<string, Literal[][]>();

  for (const item of simulated) {
    const snapshot = item.state.toLiterals();
    if (item.exit.kind === 'return') {
      const group = returnGroups.get(item.exit.tag) ?? [];
      group.push(snapshot);
      returnGroups.set(item.exit.tag, group);
    } else {
      const group = throwGroups.get(item.exit.label) ?? [];
      group.push(snapshot);
      throwGroups.set(item.exit.label, group);
    }
  }

  const returns: Record<ReturnTag, Literal[]> = {};
  for (const [tag, group] of returnGroups) {
    returns[tag] = intersectLiterals(group);
  }
  const throws: Record<string, Literal[]> = {};
  for (const [label, group] of throwGroups) {
    throws[label] = intersectLiterals(group);
  }
  return { returns, throws };
}

/**
 * Verify an implementation model against a declaration.
 *
 * Overload: callers passing the compiled contract skip recompilation.
 */
export function verifyContract(
  declaration: FlowModel,
  implementation: FlowModel,
): VerificationResult;
export function verifyContract(
  declaration: CompiledContract,
  implementation: FlowModel,
): VerificationResult;
export function verifyContract(
  declaration: FlowModel | CompiledContract,
  implementation: FlowModel,
): VerificationResult {
  const contract =
    'nodes' in declaration ? compileDeclaration(declaration as FlowModel) : declaration;

  const structuralPaths = enumeratePaths(implementation);
  const failures: PathFailure[] = [];
  const simulated: SimulatedPath[] = [];

  structuralPaths.forEach((path, pathIndex) => {
    const result = simulate(pathIndex, path, contract);
    if (result === null) {
      return;
    }
    simulated.push(result);

    if (result.exit.kind === 'return') {
      const required = contract.returns[result.exit.tag];
      if (required === undefined) {
        failures.push({
          pathIndex,
          path: nodeIds(path),
          exit: result.exit,
          missing: [],
          reason: 'return-unsatisfied',
        });
        return;
      }
      const missing = unsatisfied(required, result.state);
      if (missing.length > 0) {
        failures.push({
          pathIndex,
          path: nodeIds(path),
          exit: result.exit,
          missing,
          reason: 'return-unsatisfied',
        });
      }
    } else {
      const required = contract.throws[result.exit.label];
      if (required === undefined) {
        failures.push({
          pathIndex,
          path: nodeIds(path),
          exit: result.exit,
          missing: [],
          reason: 'throw-unsatisfied',
        });
        return;
      }
      const missing = unsatisfied(required, result.state);
      if (missing.length > 0) {
        failures.push({
          pathIndex,
          path: nodeIds(path),
          exit: result.exit,
          missing,
          reason: 'throw-unsatisfied',
        });
      }
    }
  });

  if (failures.length > 0) {
    return { ok: false, pathCount: structuralPaths.length, failures, summary: null };
  }

  const observed = summarizeExitStates(simulated);
  const returnTags = Object.keys(contract.returns);

  // Every declared exit must be realized by at least one feasible path.
  for (const tag of returnTags) {
    if (observed.returns[tag] === undefined) {
      failures.push({
        pathIndex: -1,
        path: [],
        missing: [],
        reason: 'declared-exit-unreachable',
      });
    }
  }
  for (const label of Object.keys(contract.throws)) {
    if (observed.throws[label] === undefined) {
      failures.push({
        pathIndex: -1,
        path: [],
        missing: [],
        reason: 'declared-exit-unreachable',
      });
    }
  }

  if (failures.length > 0) {
    return { ok: false, pathCount: structuralPaths.length, failures, summary: null };
  }

  const summary: VerifiedSummary = {
    [VERIFIED_BRAND]: true,
    contract: contract.name,
    precondition: contract.precondition,
    returnTags,
    returns: observed.returns,
    throws: observed.throws,
  };

  return {
    ok: true,
    pathCount: structuralPaths.length,
    feasiblePathCount: simulated.length,
    summary,
  };
}

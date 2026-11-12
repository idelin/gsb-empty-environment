/**
 * Path-wise implication verifier.
 *
 * For every concrete input valuation satisfying the declaration preconditions,
 * the finite control-flow graph is explored path by path. A path verifies when
 * its normal return label is allowed by the contract and every exit
 * post-condition holds (including object state on exception exits). Any single
 * violating path yields a counterexample and the contract id is poisoned so the
 * failed annotation cannot propagate to callers.
 *
 * Runtime mutual recursion: callee edges resolve summaries through
 * `registry.ts`, which calls back into {@link verifyImplication} for
 * on-demand verification of mutually recursive contracts.
 */
import { satisfyingValuations, type DomainValue } from "./domain.ts";
import { evaluatePredicate } from "./predicate.ts";
import type {
  AbstractState,
  CfgNode,
  Contract,
  ExitSpec,
  Implementation,
  NodeId,
  Ref,
} from "./model.ts";
import { applyEffectsBlock, readRef, writeRef } from "./model.ts";
import { isPoisoned, lookupSummary, markPending, markPoison, registerSummary } from "./registry.ts";
import {
  sealSummary,
  type ExitSummary,
  type PathCounterexample,
  type VerificationResult,
} from "./summary.ts";

export interface VerifyOptions {
  /** Maximum number of branch/back edges unrolled per input valuation. */
  readonly maxSteps?: number;
  /** Maximum number of counterexamples retained in a failure result. */
  readonly maxCounterexamples?: number;
}

const DEFAULT_MAX_STEPS = 256;
const DEFAULT_MAX_COUNTEREXAMPLES = 20;

interface SearchFrame {
  readonly state: AbstractState;
  readonly nodeId: NodeId;
  readonly nodePath: ReadonlyArray<string>;
  readonly steps: number;
}

interface ExitObservation {
  readonly tag: string;
  readonly state: AbstractState;
  readonly nodePath: ReadonlyArray<string>;
  readonly returnedValue?: DomainValue;
}

function buildInitialState(
  valuation: Record<string, DomainValue>,
  implementation: Implementation,
): { state: AbstractState; ok: boolean; detail?: string } {
  const entry = implementation.nodes[implementation.entry];
  if (!entry || entry.kind !== "entry") {
    return { state: {}, ok: false, detail: `entry node '${implementation.entry}' is missing or not an entry node` };
  }
  return { state: { ...valuation }, ok: true };
}

function contractExit(contract: Contract, tag: string): ExitSpec | undefined {
  return contract.exits.find((exit) => exit.tag === tag);
}

function checkExit(
  contract: Contract,
  observation: ExitObservation,
): PathCounterexample | null {
  const spec = contractExit(contract, observation.tag);
  if (!spec) {
    return {
      nodePath: observation.nodePath,
      input: {},
      exitTag: observation.tag,
      reason: "missing-contract-exit",
      detail: `implementation reaches exit '${observation.tag}' that the contract does not declare`,
    };
  }
  if (
    observation.tag === "return" &&
    spec.allowedLabels &&
    spec.allowedLabels.length > 0 &&
    observation.returnedValue !== undefined &&
    !spec.allowedLabels.includes(String(observation.returnedValue))
  ) {
    return {
      nodePath: observation.nodePath,
      input: {},
      exitTag: observation.tag,
      reason: "return-label-not-allowed",
      detail: `return label '${String(observation.returnedValue)}' is not one of [${spec.allowedLabels.join(", ")}]`,
    };
  }
  for (const postCondition of spec.post) {
    if (!evaluatePredicate(postCondition, observation.state)) {
      return {
        nodePath: observation.nodePath,
        input: {},
        exitTag: observation.tag,
        reason: "post-condition-failed",
        detail: `post-condition of exit '${observation.tag}' does not hold`,
      };
    }
  }
  return null;
}

function bindArgs(args: ReadonlyArray<Ref>, state: AbstractState): AbstractState {
  const next = { ...state };
  args.forEach((ref, index) => {
    next[`__arg${index}`] = readRef(ref, state);
  });
  return next;
}

function validateReferences(implementation: Implementation): PathCounterexample | null {
  const known = new Set(Object.keys(implementation.nodes));
  if (!known.has(implementation.entry)) {
    return {
      nodePath: [implementation.entry],
      input: {},
      exitTag: "<structural>",
      reason: "malformed-model",
      detail: `entry node '${implementation.entry}' is not present in the node table`,
    };
  }
  for (const node of Object.values(implementation.nodes)) {
    const targets: NodeId[] =
      node.kind === "entry"
        ? [node.next]
        : node.kind === "branch"
          ? [node.trueNext, node.falseNext]
          : node.kind === "effects"
            ? [node.next]
            : node.kind === "call"
              ? [node.next, ...(node.onThrow ? [node.onThrow] : [])]
              : node.kind === "throw" && node.handler
                ? [node.handler]
                : [];
    for (const target of targets) {
      if (!known.has(target)) {
        return {
          nodePath: [node.id],
          input: {},
          exitTag: "<structural>",
          reason: "malformed-model",
          detail: `node '${node.id}' points at unknown node '${target}'`,
        };
      }
    }
  }
  return null;
}

function searchOneInput(
  contract: Contract,
  implementation: Implementation,
  valuation: Record<string, DomainValue>,
  maxSteps: number,
  maxCounterexamples: number,
): Array<PathCounterexample> {
  const counterexamples: PathCounterexample[] = [];
  const initial = buildInitialState(valuation, implementation);
  if (!initial.ok) {
    return [
      {
        nodePath: [implementation.entry],
        input: valuation,
        exitTag: "<structural>",
        reason: "malformed-model",
        detail: initial.detail ?? "malformed entry",
      },
    ];
  }

  const stack: SearchFrame[] = [
    { state: initial.state, nodeId: implementation.entry, nodePath: [implementation.entry], steps: 0 },
  ];

  const pushCounterexample = (counterexample: PathCounterexample): boolean => {
    counterexamples.push({ ...counterexample, input: valuation });
    return counterexamples.length >= maxCounterexamples;
  };

  while (stack.length > 0) {
    const frame = stack.pop()!;
    if (frame.steps > maxSteps) {
      if (
        pushCounterexample({
          nodePath: frame.nodePath,
          input: valuation,
          exitTag: "<bounded>",
          reason: "malformed-model",
          detail: `path exceeded ${maxSteps} steps; model is not finitely bounded`,
        })
      ) {
        return counterexamples;
      }
      continue;
    }
    const node: CfgNode | undefined = implementation.nodes[frame.nodeId];
    if (!node) {
      pushCounterexample({
        nodePath: frame.nodePath,
        input: valuation,
        exitTag: "<structural>",
        reason: "malformed-model",
        detail: `missing node '${frame.nodeId}'`,
      });
      continue;
    }

    if (node.kind === "entry") {
      stack.push({
        state: frame.state,
        nodeId: node.next,
        nodePath: [...frame.nodePath, node.next],
        steps: frame.steps + 1,
      });
      continue;
    }

    if (node.kind === "branch") {
      if (evaluatePredicate(node.guard, frame.state)) {
        stack.push({
          state: frame.state,
          nodeId: node.trueNext,
          nodePath: [...frame.nodePath, node.trueNext],
          steps: frame.steps + 1,
        });
      } else {
        stack.push({
          state: frame.state,
          nodeId: node.falseNext,
          nodePath: [...frame.nodePath, node.falseNext],
          steps: frame.steps + 1,
        });
      }
      continue;
    }

    if (node.kind === "effects") {
      const block = applyEffectsBlock(node.effects, frame.state);
      if (!block.feasible) {
        pushCounterexample({
          nodePath: frame.nodePath,
          input: valuation,
          exitTag: "<infeasible>",
          reason: "infeasible-assumption",
          detail: `assumption inside node '${node.id}' failed on a feasible input`,
        });
        continue;
      }
      stack.push({
        state: block.state,
        nodeId: node.next,
        nodePath: [...frame.nodePath, node.next],
        steps: frame.steps + 1,
      });
      continue;
    }

    if (node.kind === "call") {
      const summary = lookupSummary(node.callee);
      if (!summary || isPoisoned(node.callee)) {
        if (
          pushCounterexample({
            nodePath: frame.nodePath,
            input: valuation,
            exitTag: "<call>",
            reason: "unverified-callee",
            detail: `callee '${node.callee}' has no verified summary`,
          })
        ) {
          return counterexamples;
        }
        continue;
      }
      const callState = bindArgs(node.args, frame.state);
      for (const exit of summary.exits) {
        const stateAfterCall: AbstractState = { ...callState };
        if (exit.tag === "return") {
          if (node.bindResult) {
            writeRef(node.bindResult, stateAfterCall, exit.label);
          }
          stack.push({
            state: stateAfterCall,
            nodeId: node.next,
            nodePath: [...frame.nodePath, node.next],
            steps: frame.steps + 1,
          });
        } else if (node.onThrow) {
          if (node.bindObject) {
            writeRef(node.bindObject, stateAfterCall, exit.label);
          }
          stack.push({
            state: stateAfterCall,
            nodeId: node.onThrow,
            nodePath: [...frame.nodePath, node.onThrow],
            steps: frame.steps + 1,
          });
        } else {
          if (
            pushCounterexample({
              nodePath: frame.nodePath,
              input: valuation,
              exitTag: exit.tag,
              reason: "unhandled-throw",
              detail: `callee '${node.callee}' may throw '${exit.tag}' without a handler`,
            })
          ) {
            return counterexamples;
          }
        }
      }
      continue;
    }

    if (node.kind === "throw") {
      if (!node.handler) {
        const violation = checkExit(contract, {
          tag: node.tag,
          state: frame.state,
          nodePath: frame.nodePath,
        });
        if (violation && pushCounterexample(violation)) {
          return counterexamples;
        }
        continue;
      }
      stack.push({
        state: frame.state,
        nodeId: node.handler,
        nodePath: [...frame.nodePath, node.handler],
        steps: frame.steps + 1,
      });
      continue;
    }

    const returnedValue = readRef(node.ref, frame.state);
    const violation = checkExit(contract, {
      tag: "return",
      state: frame.state,
      nodePath: frame.nodePath,
      returnedValue,
    });
    if (violation && pushCounterexample(violation)) {
      return counterexamples;
    }
  }

  return counterexamples;
}

function toExitSummaries(contract: Contract): ReadonlyArray<ExitSummary> {
  return contract.exits.map((exit) => ({
    tag: exit.tag,
    label: exit.tag === "return" ? (exit.allowedLabels?.[0] ?? "unknown") : exit.tag,
    allowedLabels: exit.allowedLabels ?? [],
    post: exit.post as ReadonlyArray<unknown>,
  }));
}

/**
 * Verify that every feasible path of `implementation` satisfies `contract`.
 * On success the id is registered with a sealed summary; on failure it is
 * poisoned, blocking propagation of the invalid annotation.
 */
export function verifyImplication(
  contract: Contract,
  implementation: Implementation,
  options?: VerifyOptions,
): VerificationResult;
export function verifyImplication(
  contract: Contract,
  implementation: Implementation,
  options: VerifyOptions = {},
): VerificationResult {
  if (contract.id !== implementation.id) {
    throw new Error(
      `contract/implementation id mismatch: '${contract.id}' vs '${implementation.id}'`,
    );
  }
  const maxSteps = options.maxSteps ?? DEFAULT_MAX_STEPS;
  const maxCounterexamples = options.maxCounterexamples ?? DEFAULT_MAX_COUNTEREXAMPLES;

  const structural = validateReferences(implementation);
  if (structural) {
    markPoison(contract.id, structural.detail);
    return { ok: false, id: contract.id, counterexamples: [structural] };
  }

  markPending(contract.id);
  const inputs = satisfyingValuations(contract.domains, contract.pre);
  if (inputs.length === 0) {
    const counterexample: PathCounterexample = {
      nodePath: [implementation.entry],
      input: {},
      exitTag: "<precondition>",
      reason: "malformed-model",
      detail: "no finite-domain valuation satisfies the contract preconditions",
    };
    markPoison(contract.id, counterexample.detail);
    return { ok: false, id: contract.id, counterexamples: [counterexample] };
  }
  const counterexamples: PathCounterexample[] = [];
  for (const valuation of inputs) {
    const found = searchOneInput(
      contract,
      implementation,
      valuation,
      maxSteps,
      maxCounterexamples,
    );
    counterexamples.push(...found);
    if (counterexamples.length >= maxCounterexamples) {
      break;
    }
  }

  if (counterexamples.length > 0) {
    markPoison(contract.id, `${counterexamples.length} violating path(s)`);
    return { ok: false, id: contract.id, counterexamples };
  }

  const summary = sealSummary(contract.id, contract.pre as ReadonlyArray<unknown>, toExitSummaries(contract));
  registerSummary(summary);
  return { ok: true, id: contract.id, summary };
}

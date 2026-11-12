/**
 * Finite control-flow model and contract declarations.
 *
 * Runtime mutual recursion: this module imports block stepping from
 * `effects.ts`, while `effects.ts` imports the reference read/write helpers
 * ({@link readRef}, {@link writeRef}) defined here. ESM live bindings resolve
 * the cycle because the helpers are only invoked after module evaluation.
 */
import type { DomainDecl, DomainValue } from "./domain.ts";
import type { Predicate } from "./predicate.ts";
import { stepBlock, type Effect } from "./effects.ts";

export type Ref =
  | { readonly kind: "field"; readonly field: string }
  | { readonly kind: "arg"; readonly index: number }
  | { readonly kind: "ret" };

export type AbstractState = Record<string, DomainValue>;

export function readRef(ref: Ref, state: AbstractState): DomainValue {
  if (ref.kind === "field") {
    const value = state[ref.field];
    if (value === undefined) {
      throw new Error(`unbound field '${ref.field}' during state read`);
    }
    return value;
  }
  if (ref.kind === "ret") {
    const value = state.__ret;
    if (value === undefined) {
      throw new Error("return register read before it was bound");
    }
    return value;
  }
  const value = state[`__arg${ref.index}`];
  if (value === undefined) {
    throw new Error(`argument #${ref.index} read before it was bound`);
  }
  return value;
}

export function writeRef(ref: Ref, state: AbstractState, value: DomainValue): AbstractState {
  if (ref.kind === "field") {
    return { ...state, [ref.field]: value };
  }
  if (ref.kind === "ret") {
    return { ...state, __ret: value };
  }
  return { ...state, [`__arg${ref.index}`]: value };
}

export type NodeId = string;

export interface EntryNode {
  readonly kind: "entry";
  readonly id: NodeId;
  readonly next: NodeId;
}

export interface BranchNode {
  readonly kind: "branch";
  readonly id: NodeId;
  readonly guard: Predicate;
  readonly trueNext: NodeId;
  readonly falseNext: NodeId;
}

export interface EffectsNode {
  readonly kind: "effects";
  readonly id: NodeId;
  readonly effects: ReadonlyArray<Effect>;
  readonly next: NodeId;
}

export interface CallNode {
  readonly kind: "call";
  readonly id: NodeId;
  readonly callee: string;
  readonly args: ReadonlyArray<Ref>;
  readonly bindResult?: Ref;
  readonly bindObject?: Ref;
  readonly next: NodeId;
  readonly onThrow?: NodeId;
}

export interface ReturnNode {
  readonly kind: "return";
  readonly id: NodeId;
  readonly ref: Ref;
}

export interface ThrowNode {
  readonly kind: "throw";
  readonly id: NodeId;
  readonly tag: string;
  readonly handler: NodeId | null;
}

export type CfgNode =
  | EntryNode
  | BranchNode
  | EffectsNode
  | CallNode
  | ReturnNode
  | ThrowNode;

export interface ExitSpec {
  /** "return" for normal exits; any other string is an exception tag. */
  readonly tag: string;
  /** Labels the returned value must carry one of (normal exits only). */
  readonly allowedLabels?: ReadonlyArray<string>;
  /** Post-state assertions, including object state after an exception. */
  readonly post: ReadonlyArray<Predicate>;
}

export interface Contract {
  readonly id: string;
  readonly domains: DomainDecl;
  readonly pre: ReadonlyArray<Predicate>;
  readonly exits: ReadonlyArray<ExitSpec>;
}

export interface Implementation {
  readonly id: string;
  readonly entry: NodeId;
  readonly nodes: Readonly<Record<NodeId, CfgNode>>;
}

export interface BlockStep {
  readonly feasible: boolean;
  readonly state: AbstractState;
}

export function applyEffectsBlock(
  effects: ReadonlyArray<Effect>,
  state: AbstractState,
): BlockStep {
  return stepBlock(effects, state);
}

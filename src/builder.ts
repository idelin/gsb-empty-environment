/**
 * Fluent builders and overloaded entry points. Overloads distinguish building
 * the annotation (contract) from wiring the implementation model, and the
 * caller-facing {@link useSummary} overloads make an unverified result a
 * compile-time error rather than a runtime surprise.
 */
import type { DomainDecl } from "./domain.ts";
import type { Predicate } from "./predicate.ts";
import { tt } from "./predicate.ts";
import type {
  CfgNode,
  Contract,
  ExitSpec,
  Implementation,
  NodeId,
  Ref,
} from "./model.ts";
import { verifyImplication, type VerifyOptions } from "./verifier.ts";
import { lookupSummary } from "./registry.ts";
import type {
  VerificationFailure,
  VerificationResult,
  VerificationSuccess,
  VerifiedSummary,
} from "./summary.ts";

export function ref(field: string): Ref {
  return { kind: "field", field };
}

export function arg(index: number): Ref {
  return { kind: "arg", index };
}

export const ret: Ref = { kind: "ret" };

export function labelRef(target: Ref, label: string): {
  readonly kind: "setLabel";
  readonly target: Ref;
  readonly label: string;
} {
  return { kind: "setLabel", target, label };
}

export interface ContractBuilder {
  readonly id: string;
  pre(...predicates: ReadonlyArray<Predicate | ReadonlyArray<Predicate>>): ContractBuilder;
  returns(allowedLabels: ReadonlyArray<string>, post?: ReadonlyArray<Predicate>): ContractBuilder;
  throws(tag: string, post: ReadonlyArray<Predicate>): ContractBuilder;
  build(): Contract;
}

export function annotate(id: string, domains: DomainDecl): ContractBuilder;
export function annotate(contract: Contract): Contract;
export function annotate(
  first: string | Contract,
  domains?: DomainDecl,
): ContractBuilder | Contract {
  if (typeof first !== "string") {
    return first;
  }
  const id = first;
  const preconditions: Predicate[] = [];
  const exits: ExitSpec[] = [];
  const builder: ContractBuilder = {
    id,
    pre(...predicates: ReadonlyArray<Predicate | ReadonlyArray<Predicate>>): ContractBuilder {
      for (const predicate of predicates) {
        if (Array.isArray(predicate)) {
          preconditions.push(...predicate);
        } else {
          preconditions.push(predicate);
        }
      }
      return builder;
    },
    returns(allowedLabels, post = []): ContractBuilder {
      exits.push({ tag: "return", allowedLabels, post });
      return builder;
    },
    throws(tag, post): ContractBuilder {
      exits.push({ tag, allowedLabels: [], post });
      return builder;
    },
    build(): Contract {
      return {
        id,
        domains: domains ?? {},
        pre: preconditions.length > 0 ? preconditions : [tt],
        exits,
      };
    },
  };
  return builder;
}

export interface ImplBuilder {
  node(node: CfgNode): ImplBuilder;
  build(): Implementation;
}

export function implement(id: string, entry: NodeId): ImplBuilder {
  const nodes: Record<NodeId, CfgNode> = {};
  const builder: ImplBuilder = {
    node(node: CfgNode): ImplBuilder {
      if (nodes[node.id]) {
        throw new Error(`duplicate node id '${node.id}' in implementation '${id}'`);
      }
      nodes[node.id] = node;
      return builder;
    },
    build(): Implementation {
      return { id, entry, nodes };
    },
  };
  return builder;
}

export function verify(
  contract: Contract,
  implementation: Implementation,
  options?: VerifyOptions,
): VerificationResult {
  return verifyImplication(contract, implementation, options);
}

export function requireVerified(result: VerificationResult): VerifiedSummary {
  if (!result.ok) {
    throw new Error(
      `contract '${result.id}' failed verification with ${result.counterexamples.length} counterexample(s)`,
    );
  }
  return result.summary;
}

/**
 * Caller gate. The branded overload accepts only sealed summaries; passing a
 * failure result (which lacks the brand) is rejected at compile time. The
 * convenience overload unwraps a successful {@link VerificationResult}.
 */
export function useSummary(summary: VerifiedSummary): VerifiedSummary;
export function useSummary(result: VerificationSuccess): VerifiedSummary;
export function useSummary(
  input: VerifiedSummary | VerificationSuccess,
): VerifiedSummary {
  if ("__verifiedBrand" in input) {
    return input;
  }
  return input.summary;
}

export function summaryOf(id: string): VerifiedSummary | null {
  return lookupSummary(id);
}

export type { VerificationFailure, VerificationSuccess };

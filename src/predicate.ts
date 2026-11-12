/**
 * First-order predicates over finite-domain valuations. Predicates describe
 * input constraints, branch guards and post-state assertions.
 *
 * Runtime mutual recursion: `forAll`/`exists` expand inline finite domains via
 * {@link domainValues} imported from `domain.ts`, which itself imports this
 * module's {@link evaluatePredicate}.
 */
import { domainValues, type Domain, type DomainValue } from "./domain.ts";

export type Operand =
  | { readonly kind: "field"; readonly field: string }
  | { readonly kind: "const"; readonly value: DomainValue };

export type CompareOp = "eq" | "neq" | "lt" | "lte" | "gt" | "gte";

export type Predicate =
  | { readonly kind: "tt" }
  | { readonly kind: "not"; readonly inner: Predicate }
  | { readonly kind: "and"; readonly parts: ReadonlyArray<Predicate> }
  | { readonly kind: "or"; readonly parts: ReadonlyArray<Predicate> }
  | {
      readonly kind: "compare";
      readonly op: CompareOp;
      readonly left: Operand;
      readonly right: Operand;
    }
  | {
      readonly kind: "forAll";
      readonly field: string;
      readonly domain: Domain;
      readonly body: Predicate;
    }
  | {
      readonly kind: "exists";
      readonly field: string;
      readonly domain: Domain;
      readonly body: Predicate;
    };

export const tt: Predicate = { kind: "tt" };

export function field(name: string): Operand {
  return { kind: "field", field: name };
}

export function constOperand(value: DomainValue): Operand {
  return { kind: "const", value };
}

export function compare(op: CompareOp, left: Operand, right: Operand): Predicate {
  return { kind: "compare", op, left, right };
}

export function not(inner: Predicate): Predicate {
  return { kind: "not", inner };
}

export function and(...parts: ReadonlyArray<Predicate>): Predicate {
  return { kind: "and", parts };
}

export function or(...parts: ReadonlyArray<Predicate>): Predicate {
  return { kind: "or", parts };
}

function resolve(operand: Operand, valuation: Readonly<Record<string, DomainValue>>): DomainValue {
  if (operand.kind === "const") {
    return operand.value;
  }
  const value = valuation[operand.field];
  if (value === undefined) {
    throw new Error(`unbound field '${operand.field}' during predicate evaluation`);
  }
  return value;
}

function applyCompare(op: CompareOp, left: DomainValue, right: DomainValue): boolean {
  switch (op) {
    case "eq":
      return left === right;
    case "neq":
      return left !== right;
    case "lt":
      return left < right;
    case "lte":
      return left <= right;
    case "gt":
      return left > right;
    case "gte":
      return left >= right;
  }
}

export function evaluatePredicate(
  predicate: Predicate,
  valuation: Readonly<Record<string, DomainValue>>,
): boolean {
  switch (predicate.kind) {
    case "tt":
      return true;
    case "not":
      return !evaluatePredicate(predicate.inner, valuation);
    case "and":
      return predicate.parts.every((part) => evaluatePredicate(part, valuation));
    case "or":
      return predicate.parts.some((part) => evaluatePredicate(part, valuation));
    case "compare":
      return applyCompare(
        predicate.op,
        resolve(predicate.left, valuation),
        resolve(predicate.right, valuation),
      );
    case "forAll":
      return domainValues(predicate.domain).every((value) =>
        evaluatePredicate(predicate.body, { ...valuation, [predicate.field]: value }),
      );
    case "exists":
      return domainValues(predicate.domain).some((value) =>
        evaluatePredicate(predicate.body, { ...valuation, [predicate.field]: value }),
      );
  }
}

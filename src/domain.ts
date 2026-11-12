/**
 * Finite semantic domains. Every field referenced by a contract or a control
 * flow model must belong to a finite domain so that path-wise verification can
 * enumerate concrete input valuations.
 *
 * Runtime mutual recursion: this module imports {@link evaluatePredicate} from
 * `predicate.ts` while `predicate.ts` imports {@link domainValues} from here.
 */
import { evaluatePredicate, type Predicate } from "./predicate.ts";

export type DomainValue = boolean | string | number;

export type Domain =
  | { readonly kind: "bool" }
  | { readonly kind: "label"; readonly labels: ReadonlyArray<string> }
  | { readonly kind: "range"; readonly min: number; readonly max: number }
  | { readonly kind: "enum"; readonly values: ReadonlyArray<string> };

export interface DomainDecl {
  readonly [field: string]: Domain;
}

export type Valuation = Readonly<Record<string, DomainValue>>;

export function domainValues(domain: Domain): DomainValue[] {
  switch (domain.kind) {
    case "bool":
      return [false, true];
    case "label":
      return [...domain.labels];
    case "enum":
      return [...domain.values];
    case "range": {
      const values: number[] = [];
      for (let value = domain.min; value <= domain.max; value += 1) {
        values.push(value);
      }
      return values;
    }
  }
}

/** Cartesian product of every declared finite domain. */
export function enumerateValuations(decls: DomainDecl): Array<Record<string, DomainValue>> {
  const entries = Object.entries(decls);
  let valuations: Array<Record<string, DomainValue>> = [{}];
  for (const [field, domain] of entries) {
    const next: Array<Record<string, DomainValue>> = [];
    for (const partial of valuations) {
      for (const value of domainValues(domain)) {
        next.push({ ...partial, [field]: value });
      }
    }
    valuations = next;
  }
  return valuations;
}

/**
 * Enumerate the valuations that satisfy every declaration precondition.
 * Calls back into the predicate module, closing the mutual recursion cycle.
 */
export function satisfyingValuations(
  decls: DomainDecl,
  predicates: ReadonlyArray<Predicate>,
): Array<Record<string, DomainValue>> {
  return enumerateValuations(decls).filter((valuation) =>
    predicates.every((predicate) => evaluatePredicate(predicate, valuation)),
  );
}

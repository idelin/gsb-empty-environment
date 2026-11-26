/**
 * Minimal, deliberately cache-free small-graph expander.
 *
 * It consumes a `ResolutionResult` and expands credential-like objects so the
 * security tests have an independent reference path that never touches the
 * semantic cache. Only the enforced feature set is supported:
 * term/vocab/base expansion, `@type: "@id" | "@vocab"` coercion and
 * `@reverse` handling.
 */

import { raise } from "./diagnostic.ts";
import type {
  ResolutionResult,
  ResolvedTerm,
} from "./types.ts";

export type ExpandedValue =
  | string
  | number
  | boolean
  | null
  | ExpandedNode
  | ExpandedValue[];

export interface ExpandedNode {
  readonly [property: string]: ExpandedValue;
}

export function expandSmallGraph(
  graph: Record<string, unknown>,
  resolution: ResolutionResult,
): ExpandedNode {
  const output: Record<string, ExpandedValue> = {};
  for (const [key, value] of Object.entries(graph)) {
    const term = resolution.terms.get(key);
    if (term === undefined) {
      raise({
        code: "UNKNOWN_KEYWORD_REWRITE",
        message: "Graph uses a term absent from the resolved context",
        offendingTerm: key,
        detail: "small-graph expansion rejects undeclared terms",
      });
    }
    const expanded = expandValue(value, term, resolution);
    const bucket = term.reverse ? `@reverse:${term.iri}` : term.iri;
    const prior = output[bucket];
    output[bucket] = prior === undefined ? expanded : mergeValues(prior, expanded);
  }
  return output;
}

function expandValue(
  value: unknown,
  term: ResolvedTerm,
  resolution: ResolutionResult,
): ExpandedValue {
  if (Array.isArray(value)) {
    return value.map((item) => expandScalar(item, term, resolution));
  }
  return expandScalar(value, term, resolution);
}

function expandScalar(
  value: unknown,
  term: ResolvedTerm,
  resolution: ResolutionResult,
): ExpandedValue {
  if (value !== null && typeof value === "object") {
    if (Array.isArray(value)) {
      raise({
        code: "CONTENT_MALFORMED",
        message: "Nested arrays are not supported by the small-graph expander",
        offendingTerm: term.term,
      });
    }
    return expandSmallGraph(value as Record<string, unknown>, resolution);
  }
  if (term.forcedType === "@id" || term.forcedType === "@vocab") {
    if (typeof value !== "string") {
      raise({
        code: "CONTENT_MALFORMED",
        message: `Forced ${term.forcedType} value must be a string`,
        offendingTerm: term.term,
      });
    }
    return coerceIri(value, term, resolution);
  }
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
    return value;
  }
  raise({
    code: "CONTENT_MALFORMED",
    message: "Value has an unsupported type",
    offendingTerm: term.term,
  });
}

function coerceIri(
  value: string,
  term: ResolvedTerm,
  resolution: ResolutionResult,
): string {
  if (term.forcedType === "@vocab") {
    return resolution.vocab === null
      ? value
      : value.includes(":")
        ? value
        : `${resolution.vocab}${value}`;
  }
  if (/^[A-Za-z][A-Za-z0-9+.-]*:/.test(value)) return value;
  return resolution.base === null ? value : new URL(value, resolution.base).toString();
}

function mergeValues(existing: ExpandedValue, incoming: ExpandedValue): ExpandedValue {
  const left = Array.isArray(existing) ? existing : [existing];
  const right = Array.isArray(incoming) ? incoming : [incoming];
  return [...left, ...right];
}

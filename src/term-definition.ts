/**
 * Term-level semantics: IRI expansion, forced-type enforcement, protected
 * fingerprints and collision classification.
 */

import { canonicalJson } from "./canonical.ts";
import { raise } from "./diagnostic.ts";
import type {
  ContentId,
  ExpandedTermDefinition,
  ForcedType,
  ProtectedRegistration,
  ResolvedTerm,
} from "./types.ts";

const KNOWN_KEYWORDS = new Set<string>([
  "@id",
  "@type",
  "@reverse",
  "@container",
  "@nest",
  "@protected",
  "@propagate",
]);

const SUPPORTED_TYPES = new Set<string>(["@id", "@vocab"]);

/** Any non-keyword member of an expanded definition is an unknown rewrite. */
export function assertKnownDefinitionKeys(
  definition: ExpandedTermDefinition,
  term: string,
  cid: ContentId,
  importPath: readonly ContentId[],
  depth: number,
): void {
  for (const key of Object.keys(definition)) {
    if (key.startsWith("@") && !KNOWN_KEYWORDS.has(key)) {
      raise({
        code: "UNKNOWN_KEYWORD_REWRITE",
        message: `Unknown JSON-LD keyword ${key} cannot rewrite protected semantics`,
        offendingTerm: term,
        offendingCid: cid,
        importPath,
        depth,
        detail: `key=${key}`,
      });
    }
  }
}

export function normaliseContainer(value: unknown): string[] {
  if (value === undefined) return [];
  if (typeof value === "string") return [value];
  if (Array.isArray(value) && value.every((item) => typeof item === "string")) {
    return [...(value as string[])];
  }
  raise({
    code: "CONTENT_MALFORMED",
    message: "@container must be a string or an array of strings",
  });
}

export function enforceForcedType(
  value: unknown,
  term: string,
  cid: ContentId,
  importPath: readonly ContentId[],
  depth: number,
): ForcedType | null {
  if (value === undefined) return null;
  if (typeof value !== "string") {
    raise({
      code: "CONTENT_MALFORMED",
      message: "@type must be a string in an expanded term definition",
      offendingTerm: term,
      offendingCid: cid,
      importPath,
      depth,
    });
  }
  if (!SUPPORTED_TYPES.has(value)) {
    raise({
      code: "UNSUPPORTED_FORCED_TYPE",
      message: `Only @id and @vocab forced types are supported, got ${value}`,
      offendingTerm: term,
      offendingCid: cid,
      importPath,
      depth,
      detail: `type=${value}`,
    });
  }
  return value as ForcedType;
}

/** Compact IRI / term expansion against the active vocab and base. */
export function expandIri(
  value: string,
  vocab: string | null,
  base: string | null,
  documentRelative: boolean,
): string {
  if (value === "") return value;
  if (isAbsoluteIri(value) || value.startsWith("@")) return value;
  const colon = value.indexOf(":");
  if (colon > 0) {
    const prefix = value.slice(0, colon);
    if (prefix in Object.prototype) return value;
    return value;
  }
  if (documentRelative) {
    return base === null ? value : joinBase(base, value);
  }
  if (vocab !== null) return vocab + value;
  return value;
}

function isAbsoluteIri(value: string): boolean {
  const colon = value.indexOf(":");
  if (colon < 2) return false;
  const scheme = value.slice(0, colon);
  return /^[A-Za-z][A-Za-z0-9+.-]*$/.test(scheme);
}

function joinBase(base: string, relative: string): string {
  try {
    return new URL(relative, base).toString();
  } catch {
    return base + relative;
  }
}

/**
 * Fingerprint of the semantic content of a protected definition. Two
 * registrations are only compatible when they originate from the exact same
 * content id; fingerprints make every other mismatch explainable.
 */
export function protectedFingerprint(registration: Omit<
  ProtectedRegistration,
  "definingCid" | "definitionFingerprint"
>): string {
  return canonicalJson({
    term: registration.term,
    iri: registration.iri,
    reverse: registration.reverse,
    forcedType: registration.forcedType ?? "",
    container: [...registration.container],
  });
}

export function resolvedFingerprint(term: ResolvedTerm): string {
  return canonicalJson({
    term: term.term,
    iri: term.iri,
    reverse: term.reverse,
    forcedType: term.forcedType ?? "",
    container: [...term.container],
  });
}

/**
 * Hard-protected collision rule.
 *
 * Re-registering a protected term is allowed only when the incoming context is
 * literally the same content id (idempotent diamond/dedup). Every other case
 * is a hard rejection — including equal values introduced by another context,
 * alias folding onto a protected IRI and reverse/forward collisions.
 */
export function classifyProtectedCollision(
  existing: ProtectedRegistration,
  candidate: {
    readonly term: string;
    readonly iri: string;
    readonly reverse: boolean;
    readonly forcedType: ForcedType | null;
    readonly container: readonly string[];
    readonly definingCid: ContentId;
  },
): "idempotent" | "redefinition" | "alias" | "reverse" {
  if (
    candidate.definingCid === existing.definingCid &&
    candidate.term === existing.term
  ) {
    return "idempotent";
  }
  if (candidate.term !== existing.term) {
    if (candidate.reverse !== existing.reverse) return "reverse";
    return "alias";
  }
  return "redefinition";
}

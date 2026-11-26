/**
 * Offline JSON-LD context resolution engine.
 *
 * Security posture:
 *  - no network access exists anywhere in the module graph;
 *  - every context read passes the signed allow-list and digest re-hash;
 *  - `@import` chains are capped at four levels and cycles fail closed;
 *  - `@propagate: false` is implemented as a full state snapshot/restore, so
 *    vocabulary, base, terms, nests and protected registrations are isolated;
 *  - protected bindings are write-once except idempotent same-cid repeats;
 *  - `@nest` expansion runs a dedicated collision pass so aliases that unpack
 *    onto the same field are rejected even when values match.
 */

import { assertContextTrusted } from "./trust-manifest.ts";
import { raise } from "./diagnostic.ts";
import { EngineState } from "./engine-state.ts";
import type { CandidateBinding } from "./engine-state.ts";
import {
  assertKnownDefinitionKeys,
  enforceForcedType,
  expandIri,
  normaliseContainer,
} from "./term-definition.ts";
import { MAX_IMPORT_DEPTH } from "./types.ts";
import type {
  AllowedContextEntry,
  ContentId,
  ContextObject,
  ContextStackFrame,
  ContentStore,
  ExpandedTermDefinition,
  ResolutionResult,
} from "./types.ts";

const CONTEXT_KEYWORDS = new Set<string>([
  "@version",
  "@base",
  "@vocab",
  "@import",
  "@propagate",
  "@protected",
]);

export interface ResolveInput {
  readonly rootCid: ContentId;
  readonly store: ContentStore;
  readonly allowed: ReadonlyMap<ContentId, AllowedContextEntry>;
}

export function resolveContext(input: ResolveInput): ResolutionResult {
  const state = new EngineState();
  const closure: ContentId[] = [];
  const stack: ContextStackFrame[] = [];

  processFrame({
    cid: input.rootCid,
    depth: 1,
    propagate: true,
    state,
    store: input.store,
    allowed: input.allowed,
    closure,
    stack,
    ancestorCids: [],
  });

  if (state.nestScopes.size > 0) {
    raise({
      code: "NEST_SCOPE_CONFLICT",
      message: "Unresolved @nest scope remains after resolution",
      detail: [...state.nestScopes.keys()].join(","),
    });
  }

  return {
    rootCid: input.rootCid,
    stack: [...stack],
    terms: new Map(state.terms),
    protectedTerms: new Map(state.protectedTerms),
    vocab: state.vocab,
    base: state.base,
    closure: dedupe(closure),
  };
}

interface FrameInput {
  readonly cid: ContentId;
  readonly depth: number;
  readonly propagate: boolean;
  readonly state: EngineState;
  readonly store: ContentStore;
  readonly allowed: ReadonlyMap<ContentId, AllowedContextEntry>;
  readonly closure: ContentId[];
  readonly stack: ContextStackFrame[];
  readonly ancestorCids: ContentId[];
}

function processFrame(frame: FrameInput): void {
  if (frame.depth > MAX_IMPORT_DEPTH) {
    raise({
      code: "IMPORT_DEPTH_EXCEEDED",
      message: `@import chain exceeds maximum depth of ${MAX_IMPORT_DEPTH}`,
      offendingCid: frame.cid,
      importPath: [...frame.ancestorCids, frame.cid],
      depth: frame.depth,
    });
  }
  if (frame.ancestorCids.includes(frame.cid)) {
    raise({
      code: "IMPORT_CYCLE_DETECTED",
      message: "Cyclic @import chain is forbidden in offline resolution",
      offendingCid: frame.cid,
      importPath: [...frame.ancestorCids, frame.cid],
      depth: frame.depth,
    });
  }

  const object = frame.store.get(frame.cid);
  assertContextTrusted(frame.cid, object, frame.allowed, frame.store);
  const context = object as ContextObject;
  frame.closure.push(frame.cid);
  frame.stack.push({
    cid: frame.cid,
    depth: frame.depth,
    propagate: frame.propagate,
  });

  const framePropagate = readBooleanKeyword(context["@propagate"], frame, "@propagate");
  const scoped = framePropagate === false;
  const snapshot = scoped ? frame.state.snapshot() : null;

  try {
    for (const importCid of readImportTargets(context, frame)) {
      processFrame({
        ...frame,
        cid: importCid,
        depth: frame.depth + 1,
        propagate: true,
        ancestorCids: [...frame.ancestorCids, frame.cid],
      });
    }

    applyScopeKeywords(context, frame);

    const frameProtected =
      readBooleanKeyword(context["@protected"], frame, "@protected") === true;

    for (const term of Object.keys(context)) {
      if (CONTEXT_KEYWORDS.has(term)) continue;
      processTerm(term, context[term], frame, frameProtected);
    }

    expandNestContainers(frame);
  } finally {
    if (scoped && snapshot !== null) {
      frame.state.restore(snapshot);
    }
  }
}

function readImportTargets(
  context: ContextObject,
  frame: FrameInput,
): ContentId[] {
  const value = context["@import"];
  if (value === undefined) return [];
  const targets = Array.isArray(value) ? value : [value];
  for (const target of targets) {
    if (typeof target !== "string" || target.length === 0) {
      raise({
        code: "IMPORT_TARGET_MALFORMED",
        message: "@import must reference content ids; URLs are not accepted",
        offendingCid: frame.cid,
        importPath: [...frame.ancestorCids, frame.cid],
        depth: frame.depth,
      });
    }
  }
  return targets as ContentId[];
}

function readBooleanKeyword(
  value: unknown,
  frame: FrameInput,
  keyword: string,
): boolean | null {
  if (value === undefined) return null;
  if (typeof value !== "boolean") {
    raise({
      code: "CONTENT_MALFORMED",
      message: `${keyword} must be a boolean`,
      offendingCid: frame.cid,
      importPath: [...frame.ancestorCids, frame.cid],
      depth: frame.depth,
    });
  }
  return value;
}

function applyScopeKeywords(context: ContextObject, frame: FrameInput): void {
  if (context["@base"] !== undefined) {
    const base = context["@base"];
    if (base !== null && typeof base !== "string") {
      raise({
        code: "CONTENT_MALFORMED",
        message: "@base must be a string or null",
        offendingCid: frame.cid,
        depth: frame.depth,
      });
    }
    frame.state.base = base;
  }
  if (context["@vocab"] !== undefined) {
    const vocab = context["@vocab"];
    if (vocab !== null && typeof vocab !== "string") {
      raise({
        code: "CONTENT_MALFORMED",
        message: "@vocab must be a string or null",
        offendingCid: frame.cid,
        depth: frame.depth,
      });
    }
    frame.state.vocab =
      vocab === null
        ? null
        : expandIri(vocab, frame.state.vocab, frame.state.base, false);
  }
}

function processTerm(
  term: string,
  raw: unknown,
  frame: FrameInput,
  frameProtected: boolean,
): void {
  const path = [...frame.ancestorCids, frame.cid];
  if (term.startsWith("@")) {
    raise({
      code: "UNKNOWN_KEYWORD_REWRITE",
      message: `Unsupported context keyword ${term}`,
      offendingTerm: term,
      offendingCid: frame.cid,
      importPath: path,
      depth: frame.depth,
    });
  }

  if (typeof raw === "string") {
    frame.state.register(
      makeCandidate(frame, term, {
        iri: expandIri(raw, frame.state.vocab, frame.state.base, false),
        reverse: false,
        forcedType: null,
        container: [],
        protectedFlag: frameProtected,
        nestedUnder: [],
      }),
    );
    return;
  }

  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) {
    raise({
      code: "CONTENT_MALFORMED",
      message: "Term definition must be a string or an object",
      offendingTerm: term,
      offendingCid: frame.cid,
      importPath: path,
      depth: frame.depth,
    });
  }

  const definition = raw as ExpandedTermDefinition;
  assertKnownDefinitionKeys(definition, term, frame.cid, path, frame.depth);

  const nestValue = definition["@nest"];
  if (nestValue !== undefined) {
    const nestedUnder = normaliseNestValue(nestValue, term, frame);
    frame.state.parkInNest(nestedUnder[0] as string, {
      ...makeCandidate(frame, term, {
        iri: resolveTermIri(term, definition, frame),
        reverse: definition["@reverse"] !== undefined,
        forcedType: enforceForcedType(
          definition["@type"],
          term,
          frame.cid,
          path,
          frame.depth,
        ),
        container: normaliseContainer(definition["@container"]),
        protectedFlag: frameProtected || definition["@protected"] === true,
        nestedUnder,
      }),
    });
    return;
  }

  if (definition["@reverse"] !== undefined) {
    registerReverseTerm(term, definition, frame, frameProtected);
    return;
  }

  frame.state.register(
    makeCandidate(frame, term, {
      iri: resolveTermIri(term, definition, frame),
      reverse: false,
      forcedType: enforceForcedType(
        definition["@type"],
        term,
        frame.cid,
        path,
        frame.depth,
      ),
      container: normaliseContainer(definition["@container"]),
      protectedFlag: frameProtected || definition["@protected"] === true,
      nestedUnder: [],
    }),
  );
}

function normaliseNestValue(
  value: unknown,
  term: string,
  frame: FrameInput,
): string[] {
  if (typeof value === "string") return [value];
  if (Array.isArray(value) && value.every((item) => typeof item === "string")) {
    return [...(value as string[])];
  }
  raise({
    code: "CONTENT_MALFORMED",
    message: "@nest must name a string container",
    offendingTerm: term,
    offendingCid: frame.cid,
    depth: frame.depth,
  });
}

function registerReverseTerm(
  term: string,
  definition: ExpandedTermDefinition,
  frame: FrameInput,
  frameProtected: boolean,
): void {
  const reverse = definition["@reverse"];
  if (typeof reverse !== "string" || definition["@id"] !== undefined) {
    raise({
      code: "CONTENT_MALFORMED",
      message: "@reverse definitions use @reverse and must not also set @id",
      offendingTerm: term,
      offendingCid: frame.cid,
      depth: frame.depth,
    });
  }
  const path = [...frame.ancestorCids, frame.cid];
  frame.state.register(
    makeCandidate(frame, term, {
      iri: expandIri(reverse, frame.state.vocab, frame.state.base, false),
      reverse: true,
      forcedType: enforceForcedType(
        definition["@type"],
        term,
        frame.cid,
        path,
        frame.depth,
      ),
      container: normaliseContainer(definition["@container"]),
      protectedFlag: frameProtected || definition["@protected"] === true,
      nestedUnder: [],
    }),
  );
}

interface CandidateFields {
  readonly iri: string;
  readonly reverse: boolean;
  readonly forcedType: ReturnType<typeof enforceForcedType>;
  readonly container: readonly string[];
  readonly protectedFlag: boolean;
  readonly nestedUnder: readonly string[];
}

function makeCandidate(
  frame: FrameInput,
  term: string,
  fields: CandidateFields,
): CandidateBinding {
  return {
    term,
    iri: fields.iri,
    reverse: fields.reverse,
    forcedType: fields.forcedType,
    container: [...fields.container],
    protectedFlag: fields.protectedFlag,
    nestedUnder: [...fields.nestedUnder],
    provenance: dedupe([...frame.closure]),
    definingCid: frame.cid,
    importPath: [...frame.ancestorCids, frame.cid],
    depth: frame.depth,
  };
}

function resolveTermIri(
  term: string,
  definition: ExpandedTermDefinition,
  frame: FrameInput,
): string {
  const id = definition["@id"];
  if (id === undefined) {
    return expandIri(term, frame.state.vocab, frame.state.base, false);
  }
  if (typeof id !== "string") {
    raise({
      code: "CONTENT_MALFORMED",
      message: "@id in term definition must be a string",
      offendingTerm: term,
      offendingCid: frame.cid,
      depth: frame.depth,
    });
  }
  return expandIri(id, frame.state.vocab, frame.state.base, id === term);
}

/**
 * Drain every parked `@nest` binding and replay it through the same protected
 * collision engine. Nested aliases that fold onto one protected field therefore
 * fail with the same diagnostics as top-level aliases.
 */
function expandNestContainers(frame: FrameInput): void {
  const scopes = [...frame.state.nestScopes.keys()];
  for (const scope of scopes) {
    const queued = frame.state.drainNest(scope);
    for (const binding of queued) {
      frame.state.register({
        ...binding,
        importPath: [...frame.ancestorCids, frame.cid],
        depth: frame.depth,
        provenance: dedupe([...binding.provenance, frame.cid]),
      });
    }
  }
}

function dedupe(values: readonly string[]): string[] {
  return [...new Set(values)];
}

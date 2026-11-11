/**
 * Safe configuration merge.
 *
 * Inputs are {@link ParsedTree} values, i.e. trees that originated from JSON
 * text via {@link ParsedTree.parse}. Everything is built inside a fresh
 * isolated tree first; the live configuration object is only replaced once
 * the whole merge has succeeded, so a rejected payload can never leave the
 * store half-updated.
 *
 * Records use a null prototype, arrays are replaced element-by-element by
 * index, dangerous key segments are rejected, and every leaf keeps the
 * JSON-pointer path of the source that last wrote it.
 */

import {
  ParsedTree,
  type JsonArray,
  type JsonRecord,
  type JsonScalar,
  type JsonValue,
} from './json-parser.ts';

/** Key segments that must never be written through dynamically. */
export const DANGEROUS_KEYS: readonly string[] = ['__proto__', 'constructor', 'prototype'];

export interface MergeSource {
  tree: ParsedTree;
  /** Overrides the tree's own label in provenance strings. */
  name?: string;
}

export interface MergeOptions {
  /**
   * When true (default), arrays sharing the same path are merged element by
   * element using integer indices. When false, an incoming array replaces the
   * previous value wholesale. Either way, non-array values are replaced.
   */
  indexReplaceArrays?: boolean;
}

export interface MergeResult {
  /** Frozen, prototype-less merged tree. */
  root: JsonRecord;
  /** JSON-pointer path of each scalar leaf -> source that wrote it. */
  origins: ReadonlyMap<string, string>;
}

export class MergeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'MergeError';
  }
}

const sourceNameOf = (source: MergeSource): string =>
  source.name ?? source.tree.sourceName;

const isRecord = (value: JsonValue): value is JsonRecord =>
  value !== null && typeof value === 'object' && !Array.isArray(value);

const isLeaf = (value: JsonValue): value is JsonScalar =>
  value === null || typeof value !== 'object';

/** RFC 6901 JSON-pointer segment encoding. */
export function encodePointerSegment(segment: string): string {
  return segment.replace(/~/g, '~0').replace(/\//g, '~1');
}

function joinPointer(base: string, segment: string): string {
  return `${base}/${encodePointerSegment(segment)}`;
}

function assertSafeKey(key: string, pointer: string): void {
  if (DANGEROUS_KEYS.includes(key)) {
    throw new MergeError(`Refusing dangerous key segment "${key}" at ${pointer}`);
  }
}

function deepFreeze(value: JsonValue): void {
  if (value === null || typeof value !== 'object') {
    return;
  }
  Object.freeze(value);
  if (Array.isArray(value)) {
    for (const item of value) {
      deepFreeze(item);
    }
    return;
  }
  for (const key of Object.keys(value)) {
    deepFreeze(value[key]);
  }
}

/** Remove every provenance entry at or below the given pointer. */
function pruneOrigins(origins: Map<string, string>, pointer: string): void {
  const prefix = `${pointer}/`;
  for (const stored of Array.from(origins.keys())) {
    if (stored === pointer || stored.startsWith(prefix)) {
      origins.delete(stored);
    }
  }
}

/**
 * Deep-copy an already-parsed tree into a fresh isolated tree, recording the
 * source of every scalar leaf. Dangerous segments are re-checked here so the
 * rule also covers any previously parsed tree handed in by callers.
 */
function cloneIntoIsolation(
  value: JsonValue,
  origins: Map<string, string>,
  pointer: string,
  sourceName: string,
): JsonValue {
  if (isLeaf(value)) {
    origins.set(pointer, sourceName);
    return value;
  }
  if (Array.isArray(value)) {
    const copy: JsonArray = [];
    value.forEach((item, index) => {
      copy[index] = cloneIntoIsolation(
        item,
        origins,
        joinPointer(pointer, String(index)),
        sourceName,
      );
    });
    return copy;
  }
  const copy: JsonRecord = Object.create(null) as JsonRecord;
  for (const key of Object.keys(value)) {
    assertSafeKey(key, pointer);
    copy[key] = cloneIntoIsolation(
      value[key],
      origins,
      joinPointer(pointer, key),
      sourceName,
    );
  }
  return copy;
}

/**
 * Merge one parsed tree into the isolated draft. The draft is only mutated
 * after every safety check for the current node has passed.
 */
function mergeInto(
  target: JsonValue | undefined,
  incoming: JsonValue,
  origins: Map<string, string>,
  pointer: string,
  sourceName: string,
  indexReplaceArrays: boolean,
): JsonValue {
  if (isLeaf(incoming)) {
    pruneOrigins(origins, pointer);
    origins.set(pointer, sourceName);
    return incoming;
  }

  if (Array.isArray(incoming)) {
    if (Array.isArray(target) && indexReplaceArrays) {
      incoming.forEach((item, index) => {
        const indexPointer = joinPointer(pointer, String(index));
        target[index] = mergeInto(
          target[index],
          item,
          origins,
          indexPointer,
          sourceName,
          indexReplaceArrays,
        );
      });
      return target;
    }
    pruneOrigins(origins, pointer);
    return cloneIntoIsolation(incoming, origins, pointer, sourceName);
  }

  const draft: JsonRecord = isRecord(target)
    ? target
    : (Object.create(null) as JsonRecord);
  if (draft !== target) {
    pruneOrigins(origins, pointer);
  }
  for (const key of Object.keys(incoming)) {
    assertSafeKey(key, pointer);
    const keyPointer = joinPointer(pointer, key);
    draft[key] = mergeInto(
      draft[key],
      incoming[key],
      origins,
      keyPointer,
      sourceName,
      indexReplaceArrays,
    );
  }
  return draft;
}

/** Structural brand check without exposing the constructor. */
export function isParsedTree(value: unknown): value is ParsedTree {
  return value instanceof ParsedTree;
}

function assertSource(source: MergeSource): asserts source is MergeSource {
  if (!source || typeof source !== 'object' || !isParsedTree(source.tree)) {
    throw new MergeError('Each merge source must contain a ParsedTree from parseJsonTree()');
  }
}

/**
 * Merge parsed JSON trees in order (later sources win). No input object is
 * mutated: all work happens in a freshly allocated isolated tree.
 */
export function mergeConfigs(
  sources: readonly MergeSource[],
  options: MergeOptions = {},
): MergeResult {
  if (!Array.isArray(sources) || sources.length === 0) {
    throw new MergeError('At least one parsed JSON source is required');
  }
  const indexReplaceArrays = options.indexReplaceArrays ?? true;
  const root: JsonRecord = Object.create(null) as JsonRecord;
  const origins = new Map<string, string>();

  for (const source of sources) {
    assertSource(source);
    if (!isRecord(source.tree.root)) {
      throw new MergeError(
        `Top-level JSON value from "${sourceNameOf(source)}" must be an object`,
      );
    }
    mergeInto(
      root,
      source.tree.root,
      origins,
      '',
      sourceNameOf(source),
      indexReplaceArrays,
    );
  }

  deepFreeze(root);
  return { root, origins };
}

/**
 * Stateful configuration holder with atomic publish semantics.
 *
 * {@link ConfigStore.publish} builds the complete result in isolation and
 * only swaps the exposed reference once every source merged successfully.
 * The previous configuration and the global prototypes are untouched on
 * failure.
 */
export class ConfigStore {
  private current: JsonRecord = Object.create(null) as JsonRecord;
  private currentOrigins = new Map<string, string>();

  /** Frozen, prototype-less active configuration. */
  get config(): JsonRecord {
    return this.current;
  }

  /** Source label of every scalar leaf in the active configuration. */
  get provenance(): ReadonlyMap<string, string> {
    return this.currentOrigins;
  }

  /** JSON-pointer path of the source that last wrote a leaf. */
  getOrigin(pointer: string): string | undefined {
    return this.currentOrigins.get(pointer);
  }

  /**
   * Merge and publish. With `accumulate` (default) the active configuration
   * participates as the first draft layer; with `accumulate: false` only the
   * supplied sources define the new configuration.
   */
  publish(
    sources: readonly MergeSource[],
    options: MergeOptions & { accumulate?: boolean } = {},
  ): MergeResult {
    const draftResult = this.buildInIsolation(sources, options);
    this.current = draftResult.root;
    this.currentOrigins = draftResult.origins;
    return draftResult;
  }

  /**
   * Run the whole merge against a throwaway draft without touching the active
   * configuration. Useful for validation or dry runs.
   */
  buildInIsolation(
    sources: readonly MergeSource[],
    options: MergeOptions & { accumulate?: boolean } = {},
  ): MergeResult {
    const accumulate = options.accumulate ?? true;
    const indexReplaceArrays = options.indexReplaceArrays ?? true;
    const draftRoot: JsonRecord = Object.create(null) as JsonRecord;
    const draftOrigins = new Map<string, string>();

    if (accumulate) {
      const baseClone = cloneIntoIsolation(
        this.current,
        draftOrigins,
        '',
        '<previous-config>',
      );
      if (!isRecord(baseClone)) {
        throw new MergeError('Internal error: accumulated configuration root must be a record');
      }
      Object.assign(draftRoot, baseClone);
    }

    for (const source of sources) {
      assertSource(source);
      if (!isRecord(source.tree.root)) {
        throw new MergeError(
          `Top-level JSON value from "${sourceNameOf(source)}" must be an object`,
        );
      }
      mergeInto(
        draftRoot,
        source.tree.root,
        draftOrigins,
        '',
        sourceNameOf(source),
        indexReplaceArrays,
      );
    }

    deepFreeze(draftRoot);
    return { root: draftRoot, origins: draftOrigins };
  }
}

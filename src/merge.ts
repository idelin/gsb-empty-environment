import { ConfigError, formatSource } from './errors.ts';
import type { ListOp, SourceEntry, SourceRef } from './origin.ts';
import { convertValue, isLeafTag, parseTag, type ParsedTag, type Schema, type SchemaNode } from './schema.ts';
import { deepEquals, maskValue, SecretValue } from './secret.ts';

export interface HistoryEntry {
  value: unknown;
  source: SourceRef;
}

interface LeafState {
  value: unknown;
  source: SourceRef;
  /** Overridden values, oldest first. */
  history: HistoryEntry[];
}

export interface ArrayElement {
  value: unknown;
  source: SourceRef;
}

export interface ArrayEvent {
  op: ListOp;
  source: SourceRef;
  values: unknown[];
  removed?: unknown[];
}

interface ArrayState {
  elements: ArrayElement[];
  /** Snapshots replaced by a later `set`, oldest first. */
  history: Array<{ elements: ArrayElement[]; source: SourceRef }>;
  events: ArrayEvent[];
}

type NodeState =
  | { kind: 'leaf'; leaf: LeafState }
  | { kind: 'array'; array: ArrayState }
  | { kind: 'object'; children: Map<string, NodeState> };

export interface MergeOptions {
  schema?: Schema;
  /** Allow keys not present in the schema. Default false when a schema is given. */
  allowUnknown?: boolean;
}

export function mergeConfig(entries: SourceEntry[], options: MergeOptions = {}): MergedConfig {
  const root: NodeState = { kind: 'object', children: new Map() };
  for (const entry of entries) applyEntry(root, entry, options);
  return new MergedConfig(root);
}

function lookupSchema(schema: Schema | undefined, path: string[]): SchemaNode | undefined {
  if (!schema) return undefined;
  let node: SchemaNode = schema;
  for (const segment of path) {
    if (isLeafTag(node)) return undefined;
    const child = (node as Record<string, SchemaNode>)[segment];
    if (child === undefined) return undefined;
    node = child;
  }
  return node;
}

function applyEntry(root: NodeState, entry: SourceEntry, options: MergeOptions): void {
  const dotted = entry.path.join('.');
  const schemaNode = lookupSchema(options.schema, entry.path);
  if (options.schema && schemaNode === undefined && !options.allowUnknown) {
    throw new ConfigError('UNKNOWN_KEY', `Key is not defined in the schema`, {
      path: dotted,
      source: entry.source,
    });
  }
  const tag: ParsedTag | undefined =
    schemaNode !== undefined && isLeafTag(schemaNode) ? parseTag(schemaNode, dotted) : undefined;

  if (entry.op !== 'set' && tag && !tag.isArray) {
    throw new ConfigError('INVALID_LIST_OP', `List operation "${entry.op}" requires an array-typed key`, {
      path: dotted,
      source: entry.source,
    });
  }

  // Resolve parent object nodes, creating them as needed.
  let node = root;
  for (const segment of entry.path.slice(0, -1)) {
    if (node.kind !== 'object') throw new Error('unreachable');
    let child = node.children.get(segment);
    if (!child || child.kind !== 'object') {
      child = { kind: 'object', children: new Map() };
      node.children.set(segment, child);
    }
    node = child;
  }
  if (node.kind !== 'object') throw new Error('unreachable');
  const key = entry.path[entry.path.length - 1];

  const isArrayTarget = tag ? tag.isArray : entry.op !== 'set' || Array.isArray(entry.value);
  if (isArrayTarget) {
    applyArrayEntry(node, key, entry, tag, dotted);
  } else {
    applyLeafEntry(node, key, entry, tag, dotted);
  }
}

function applyLeafEntry(
  parent: Extract<NodeState, { kind: 'object' }>,
  key: string,
  entry: SourceEntry,
  tag: ParsedTag | undefined,
  dotted: string,
): void {
  const value = tag
    ? convertValue(entry.value, tag, dotted, entry.source)
    : entry.value;
  const existing = parent.children.get(key);
  const history: HistoryEntry[] = [];
  if (existing?.kind === 'leaf') {
    history.push(...existing.leaf.history, { value: existing.leaf.value, source: existing.leaf.source });
  }
  parent.children.set(key, {
    kind: 'leaf',
    leaf: { value, source: entry.source, history },
  });
}

function applyArrayEntry(
  parent: Extract<NodeState, { kind: 'object' }>,
  key: string,
  entry: SourceEntry,
  tag: ParsedTag | undefined,
  dotted: string,
): void {
  const rawItems = Array.isArray(entry.value) ? entry.value : [entry.value];
  const values = tag
    ? (convertValue(entry.value, tag, dotted, entry.source) as unknown[])
    : rawItems;
  const existing = parent.children.get(key);
  let state: ArrayState;
  if (existing?.kind === 'array') {
    state = existing.array;
  } else {
    state = { elements: [], history: [], events: [] };
    parent.children.set(key, { kind: 'array', array: state });
  }
  switch (entry.op) {
    case 'set': {
      if (state.elements.length > 0 || state.events.length > 0) {
        state.history.push({ elements: state.elements, source: state.events[state.events.length - 1]?.source ?? entry.source });
      }
      state.elements = values.map((value) => ({ value, source: entry.source }));
      state.events.push({ op: 'set', source: entry.source, values });
      break;
    }
    case 'append': {
      state.elements.push(...values.map((value) => ({ value, source: entry.source })));
      state.events.push({ op: 'append', source: entry.source, values });
      break;
    }
    case 'remove': {
      const removed: unknown[] = [];
      state.elements = state.elements.filter((el) => {
        const hit = values.some((v) => deepEquals(v, el.value));
        if (hit) removed.push(el.value);
        return !hit;
      });
      state.events.push({ op: 'remove', source: entry.source, values, removed });
      break;
    }
  }
}

function buildPlain(node: NodeState): unknown {
  switch (node.kind) {
    case 'leaf':
      return node.leaf.value;
    case 'array':
      return node.array.elements.map((el) => el.value);
    case 'object': {
      const out: Record<string, unknown> = {};
      for (const [key, child] of node.children) {
        const value = buildPlain(child);
        if (value !== undefined) out[key] = value;
      }
      return out;
    }
  }
}

export interface LeafExplanation {
  kind: 'leaf';
  path: string;
  value: unknown;
  source: SourceRef;
  history: HistoryEntry[];
}

export interface ArrayExplanation {
  kind: 'array';
  path: string;
  elements: ArrayElement[];
  history: Array<{ elements: ArrayElement[]; source: SourceRef }>;
  events: ArrayEvent[];
}

export type Explanation = LeafExplanation | ArrayExplanation;

export interface DiffEntry {
  path: string;
  change: 'added' | 'removed' | 'changed';
}

export class MergedConfig {
  private readonly root: NodeState;

  constructor(root: NodeState) {
    this.root = root;
  }

  /** Plain value at a dot path (secrets stay wrapped in SecretValue). */
  get<T = unknown>(path?: string): T {
    const node = path ? this.find(path) : this.root;
    if (!node) return undefined as T;
    return buildPlain(node) as T;
  }

  /** Provenance and override chain for a leaf or array path. */
  explain(path: string): Explanation | undefined {
    const node = this.find(path);
    if (!node) return undefined;
    if (node.kind === 'leaf') {
      return { kind: 'leaf', path, value: node.leaf.value, source: node.leaf.source, history: node.leaf.history };
    }
    if (node.kind === 'array') {
      return {
        kind: 'array',
        path,
        elements: node.array.elements,
        history: node.array.history,
        events: node.array.events,
      };
    }
    return undefined;
  }

  /** Human-readable provenance report; secrets are masked. */
  explainText(path: string): string {
    const info = this.explain(path);
    if (!info) return `${path}: <not set>`;
    const lines: string[] = [];
    if (info.kind === 'leaf') {
      lines.push(`${path} = ${JSON.stringify(maskValue(info.value))}  <- ${formatSource(info.source)}`);
      for (const h of info.history) {
        lines.push(`  overridden: ${JSON.stringify(maskValue(h.value))}  <- ${formatSource(h.source)}`);
      }
    } else {
      for (const [i, el] of info.elements.entries()) {
        lines.push(`${path}[${i}] = ${JSON.stringify(maskValue(el.value))}  <- ${formatSource(el.source)}`);
      }
      for (const ev of info.events) {
        lines.push(`  ${ev.op}: ${JSON.stringify(maskValue(ev.values))}  <- ${formatSource(ev.source)}`);
      }
    }
    return lines.join('\n');
  }

  /** Fully masked plain object, safe to log or serialize. */
  toJSON(): Record<string, unknown> {
    return maskValue(buildPlain(this.root)) as Record<string, unknown>;
  }

  /** Secret-aware structural diff against another merged config. */
  diff(other: MergedConfig): DiffEntry[] {
    const out: DiffEntry[] = [];
    diffValues(this.get(), other.get(), '', out);
    return out;
  }

  private find(path: string): NodeState | undefined {
    let node = this.root;
    for (const segment of path.split('.')) {
      if (node.kind !== 'object') return undefined;
      const child = node.children.get(segment);
      if (!child) return undefined;
      node = child;
    }
    return node;
  }
}

function diffValues(a: unknown, b: unknown, path: string, out: DiffEntry[]): void {
  if (a instanceof SecretValue || b instanceof SecretValue) {
    if (!deepEquals(a, b)) out.push({ path, change: a === undefined ? 'added' : b === undefined ? 'removed' : 'changed' });
    return;
  }
  if (isPlainObject(a) && isPlainObject(b)) {
    const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
    for (const key of keys) {
      const childPath = path ? `${path}.${key}` : key;
      diffValues(
        (a as Record<string, unknown>)[key],
        (b as Record<string, unknown>)[key],
        childPath,
        out,
      );
    }
    return;
  }
  if (!deepEquals(a, b)) {
    out.push({ path, change: a === undefined ? 'added' : b === undefined ? 'removed' : 'changed' });
  }
}

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === 'object' && !Array.isArray(v) && !(v instanceof SecretValue);
}

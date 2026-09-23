import { createHash } from 'node:crypto';
import { GraphError, type ErrorCode } from './errors.ts';
import {
  assertNoEvaluationCycles,
  isComputed,
  type GraphNode,
} from './graph.ts';
import type { DefinitionRegistry } from './registry.ts';
import { Store, type LogEntry, type SetOp } from './store.ts';

export const SUPPORTED_SCHEMA_VERSION = 1;

interface CellEntry {
  kind: 'cell';
  id: string;
  value: unknown;
}

interface ComputedEntry {
  kind: 'computed';
  id: string;
  definitionId: string;
  definitionVersion: number;
  deps: string[];
  monotonic: boolean;
}

type NodeEntry = CellEntry | ComputedEntry;

export interface Envelope {
  schemaVersion: number;
  nodes: NodeEntry[];
  digest: string;
}

export type RestoreResult =
  | { ok: true }
  | { ok: false; code: ErrorCode; message: string };

/** Build a sealed envelope from raw parts (migrations, tests, tooling). */
export function seal(schemaVersion: number, nodes: unknown[]): Envelope {
  const entries = nodes as NodeEntry[];
  return { schemaVersion, nodes: entries, digest: digestOf(schemaVersion, entries) };
}

/** Deterministic JSON: object keys sorted recursively. */
function canonical(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`;
  const entries = Object.entries(value as Record<string, unknown>)
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
    .map(([key, val]) => `${JSON.stringify(key)}:${canonical(val)}`);
  return `{${entries.join(',')}}`;
}

function digestOf(schemaVersion: number, nodes: NodeEntry[]): string {
  const hash = createHash('sha256');
  hash.update(canonical({ schemaVersion, nodes }));
  return `sha256:${hash.digest('hex')}`;
}

/**
 * Serialize pure state (cell values) plus dependency evidence (computed nodes
 * as definition id/version + dep edges). Functions are never serialized.
 */
export function save(store: Store): Envelope {
  const { nodes, values } = store.graphView();
  const entries: NodeEntry[] = [];
  for (const node of nodes.values()) {
    if (node.kind === 'cell') {
      entries.push({ kind: 'cell', id: node.id, value: values.get(node.id) });
    } else {
      entries.push({
        kind: 'computed',
        id: node.id,
        definitionId: node.definitionId,
        definitionVersion: node.definitionVersion,
        deps: [...node.deps],
        monotonic: node.monotonic,
      });
    }
  }
  return {
    schemaVersion: SUPPORTED_SCHEMA_VERSION,
    nodes: entries,
    digest: digestOf(SUPPORTED_SCHEMA_VERSION, entries),
  };
}

function validateEnvelopeShape(envelope: unknown): Envelope {
  if (typeof envelope !== 'object' || envelope === null) {
    throw new GraphError('INVALID_ENVELOPE', 'envelope is not an object');
  }
  const env = envelope as Record<string, unknown>;
  if (typeof env.schemaVersion !== 'number' || !Array.isArray(env.nodes) || typeof env.digest !== 'string') {
    throw new GraphError('INVALID_ENVELOPE', 'envelope missing required fields');
  }
  return env as unknown as Envelope;
}

/**
 * Rebuild the graph from saved dependency evidence and atomically publish it.
 * On any failure the live store is left completely untouched and readable.
 * Subscriber callbacks never run during restore.
 */
export function restore(
  store: Store,
  envelope: unknown,
  registry: DefinitionRegistry,
): RestoreResult {
  try {
    const env = validateEnvelopeShape(envelope);

    if (env.schemaVersion !== SUPPORTED_SCHEMA_VERSION) {
      throw new GraphError(
        'SCHEMA_VERSION',
        `unsupported schema version: ${env.schemaVersion} (supported: ${SUPPORTED_SCHEMA_VERSION})`,
      );
    }

    if (digestOf(env.schemaVersion, env.nodes) !== env.digest) {
      throw new GraphError('INTEGRITY', 'digest mismatch: envelope was tampered with');
    }

    // Build the replacement graph in isolation; nothing touches the live store.
    const nodes = new Map<string, GraphNode>();
    const values = new Map<string, unknown>();

    for (const entry of env.nodes) {
      if (nodes.has(entry.id)) {
        throw new GraphError('DUPLICATE_NODE', `duplicate node id: ${entry.id}`);
      }
      if (entry.kind === 'cell') {
        nodes.set(entry.id, { kind: 'cell', id: entry.id });
        values.set(entry.id, entry.value);
        continue;
      }
      if (!registry.hasId(entry.definitionId)) {
        throw new GraphError(
          'UNKNOWN_DEFINITION',
          `unknown computation definition: ${entry.definitionId}`,
        );
      }
      if (!registry.resolve(entry.definitionId, entry.definitionVersion)) {
        throw new GraphError(
          'DEFINITION_VERSION',
          `definition ${entry.definitionId} has no version ${entry.definitionVersion}`,
        );
      }
      nodes.set(entry.id, {
        kind: 'computed',
        id: entry.id,
        definitionId: entry.definitionId,
        definitionVersion: entry.definitionVersion,
        deps: [...entry.deps],
        monotonic: entry.monotonic,
      });
    }

    // Dependency evidence: every edge must reference an existing node.
    for (const node of nodes.values()) {
      if (!isComputed(node)) continue;
      for (const dep of node.deps) {
        if (!nodes.has(dep)) {
          throw new GraphError(
            'MISSING_NODE',
            `node ${node.id} depends on missing node: ${dep}`,
          );
        }
      }
    }

    // Illegal evaluation cycles rejected; allowed monotonic rings pass through.
    assertNoEvaluationCycles(nodes);

    // Recompute derived state from pure cell state via the registry only.
    const bootstrap = new Store(registry);
    bootstrap.install(nodes, values);
    const computedIds = [...nodes.values()].filter(isComputed).map((n) => n.id);
    bootstrap.recomputeAll(computedIds);

    // Atomic publish: single swap, no subscriber callbacks.
    store.install(nodes, bootstrap.graphValues(), env);
    return { ok: true };
  } catch (err) {
    if (err instanceof GraphError) {
      return { ok: false, code: err.code, message: err.message };
    }
    throw err;
  }
}

/** Replay an operation log into a fresh store; yields identical state. */
export function replay(log: readonly LogEntry[], registry: DefinitionRegistry): Store {
  const store = new Store(registry);
  for (const entry of log) {
    switch (entry.kind) {
      case 'define-cell':
        store.defineCell(entry.id, entry.initial);
        break;
      case 'define-computed':
        store.defineComputed(entry.id, entry.definition, entry.deps, {
          monotonic: entry.monotonic,
        });
        break;
      case 'tx':
        store.commitOps(entry.ops.map((op) => ({ ...op }) as SetOp));
        break;
      case 'restore': {
        const result = restore(store, entry.envelope, registry);
        if (!result.ok) {
          throw new GraphError(result.code, `replay failed: ${result.message}`);
        }
        break;
      }
    }
  }
  return store;
}

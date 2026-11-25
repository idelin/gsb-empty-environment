import type {
  Attribution,
  Branch,
  BranchId,
  DeleteDirective,
  DocId,
  LeafValue,
  Node,
  NodeId,
  TreeDoc,
} from './types.ts';

export interface NodeOpts {
  id?: NodeId;
  issuer?: string;
  sig?: string;
  attribution?: Attribution;
  role?: string;
  tenant?: string;
  version?: string;
  reason?: string;
}

export interface Marker {
  __marker: true;
  kind: Node['kind'];
  id?: NodeId;
  attribution?: Attribution;
  value?: LeafValue;
  template?: string;
  role?: string;
  tenant?: string;
  version?: string;
  reason?: string;
  children?: Record<string, Marker>;
}

function authOf(opts?: NodeOpts): Attribution | undefined {
  if (opts?.attribution) return opts.attribution;
  if (opts?.issuer) return { issuer: opts.issuer, ...(opts.sig ? { sigId: opts.sig } : {}) };
  return undefined;
}

export function obj(children: Record<string, Marker>, opts: NodeOpts = {}): Marker {
  return {
    __marker: true,
    kind: 'object',
    children,
    ...(opts.id ? { id: opts.id } : {}),
    ...(authOf(opts) ? { attribution: authOf(opts) } : {}),
  };
}

export function leaf(value: LeafValue, opts: NodeOpts = {}): Marker {
  return {
    __marker: true,
    kind: 'value',
    value,
    ...(opts.id ? { id: opts.id } : {}),
    ...(authOf(opts) ? { attribution: authOf(opts) } : {}),
  };
}

/** Explicit null (present, value === null). */
export function nil(opts: NodeOpts = {}): Marker {
  return {
    __marker: true,
    kind: 'null',
    ...(opts.id ? { id: opts.id } : {}),
    ...(authOf(opts) ? { attribution: authOf(opts) } : {}),
  };
}

/** Tombstone (durable deletion). */
export function tomb(opts: NodeOpts = {}): Marker {
  return {
    __marker: true,
    kind: 'tombstone',
    ...(opts.id ? { id: opts.id } : {}),
    ...(authOf(opts) ? { attribution: authOf(opts) } : {}),
    ...(opts.reason ? { reason: opts.reason } : {}),
  };
}

export function ref(template: string, opts: NodeOpts = {}): Marker {
  return {
    __marker: true,
    kind: 'ref',
    template,
    ...(opts.id ? { id: opts.id } : {}),
    ...(authOf(opts) ? { attribution: authOf(opts) } : {}),
    ...(opts.role ? { role: opts.role } : {}),
    ...(opts.tenant ? { tenant: opts.tenant } : {}),
    ...(opts.version ? { version: opts.version } : {}),
  };
}

export interface DocOptions {
  attribution?: Attribution;
  rootId?: NodeId;
}

/** Flatten a nested marker tree into a parsed TreeDoc with immutable ids. */
export function doc(docId: DocId, rootMarker: Marker, options: DocOptions = {}): TreeDoc {
  const nodes: Record<NodeId, Node> = {};
  let counter = 0;
  const rootId = options.rootId ?? rootMarker.id ?? `${docId}:root`;

  const ensureId = (marker: Marker, path: string): NodeId => {
    if (marker.id) return marker.id;
    counter += 1;
    return `${docId}:${path || 'n'}#${counter}`;
  };

  const walk = (marker: Marker, path: string, forcedId?: NodeId): NodeId => {
    const id = forcedId ?? ensureId(marker, path);
    const attribution = marker.attribution ?? options.attribution;
    const base = { id, ...(attribution ? { attribution } : {}) };

    if (marker.kind === 'object') {
      const edges = Object.entries(marker.children ?? {}).map(([key, child]) => ({
        key,
        child: walk(child, path ? `${path}/${key}` : key),
      }));
      nodes[id] = { ...base, kind: 'object', edges } as Node;
    } else if (marker.kind === 'value') {
      nodes[id] = { ...base, kind: 'value', value: marker.value as LeafValue } as Node;
    } else if (marker.kind === 'null') {
      nodes[id] = { ...base, kind: 'null' } as Node;
    } else if (marker.kind === 'tombstone') {
      nodes[id] = {
        ...base,
        kind: 'tombstone',
        ...(marker.reason ? { reason: marker.reason } : {}),
      } as Node;
    } else if (marker.kind === 'ref') {
      nodes[id] = {
        ...base,
        kind: 'ref',
        template: marker.template as string,
        ...(marker.role ? { role: marker.role } : {}),
        ...(marker.tenant ? { tenant: marker.tenant } : {}),
        ...(marker.version ? { version: marker.version } : {}),
      } as Node;
    }
    return id;
  };

  walk(rootMarker, '', rootId);
  return { docId, root: rootId, nodes };
}

/** Build a nested object doc from flat `/a/b` path -> terminal marker. */
export function docFromPaths(
  docId: DocId,
  paths: Record<string, Marker>,
  options: DocOptions = {},
): TreeDoc {
  const root: Marker = { __marker: true, kind: 'object', children: {} };
  for (const [rawPath, terminal] of Object.entries(paths)) {
    const segments = rawPath.split('/').filter(Boolean);
    let cursor = root;
    segments.forEach((segment, index) => {
      const isLast = index === segments.length - 1;
      const children: Record<string, Marker> = (cursor.children ??= {});
      if (isLast) {
        children[segment] = terminal;
        return;
      }
      const existing = children[segment];
      if (!existing || existing.kind !== 'object') {
        const next: Marker = { __marker: true, kind: 'object', children: {} };
        children[segment] = next;
        cursor = next;
      } else {
        existing.children ??= {};
        cursor = existing;
      }
    });
  }
  return doc(docId, root, options);
}

let branchSeq = 0;

export function branch(
  branchId: BranchId,
  priority: number,
  tree: TreeDoc,
  extra: Partial<Branch> = {},
): Branch {
  branchSeq += 1;
  return { branchId, priority, doc: tree, ...extra };
}

export function del(path: string, issuer: string, extra: Partial<DeleteDirective> = {}): DeleteDirective {
  return { path, issuer, ...extra };
}

/** Reset the deterministic auto-id sequence between scenarios if desired. */
export function resetDslSequence(): void {
  branchSeq = 0;
}

export type { Branch };

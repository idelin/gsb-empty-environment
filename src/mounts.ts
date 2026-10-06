import { VfsError, ERR } from './errors.ts';
import { splitPath, joinPath, isValidName } from './pathutil.ts';
import type { TreeStore } from './tree.ts';

export interface MountSpec {
  name: string;
  path: string;
  rootId: string;
  readOnly?: boolean;
}

export interface Mount {
  name: string;
  path: string;
  rootId: string;
  readOnly: boolean;
  /** Name of the mount that owns the graft point; forms the explicit mount graph. */
  parent: string | null;
}

/**
 * Explicit mount graph. Each mount grafts an independent root node at a
 * virtual path and may be read-only. Edges are derived at add time by
 * resolving the graft parent; a mount whose root contains its own graft
 * point is rejected as a cycle.
 */
export class MountGraph {
  private byName = new Map<string, Mount>();
  private byPath = new Map<string, Mount>();
  private byRoot = new Map<string, Mount>();
  readonly rootMount: Mount;
  private tree: TreeStore;
  private onChange: () => void;

  constructor(tree: TreeStore, onChange: () => void = () => {}) {
    this.tree = tree;
    this.onChange = onChange;
    this.rootMount = { name: 'root', path: '/', rootId: tree.rootId, readOnly: false, parent: null };
    this.byName.set(this.rootMount.name, this.rootMount);
    this.byRoot.set(this.rootMount.rootId, this.rootMount);
  }

  list(): Mount[] {
    return [...this.byName.values()];
  }

  add(spec: MountSpec, resolveFn: (path: string) => string): Mount {
    const path = normalizeMountPath(spec.path);
    if (!spec.name) {
      throw new VfsError(ERR.InvalidPath, 'mount name is required');
    }
    if (this.byName.has(spec.name)) {
      throw new VfsError(ERR.MountConflict, `mount name ${spec.name} is already used`);
    }
    if (this.byPath.has(path)) {
      throw new VfsError(ERR.MountConflict, `mount path ${path} is already used`);
    }
    if (!this.tree.has(spec.rootId)) {
      throw new VfsError(ERR.NotFound, `mount root ${spec.rootId} does not exist`);
    }
    const parentPath = joinPath(splitPath(path).slice(0, -1));
    const parentNode = resolveFn(parentPath);
    if (spec.rootId === parentNode || this.tree.isAncestor(spec.rootId, parentNode)) {
      throw new VfsError(ERR.MountCycle, `mount ${spec.name} at ${path} would contain itself`);
    }
    if (this.byRoot.has(spec.rootId)) {
      throw new VfsError(ERR.MountConflict, `node ${spec.rootId} is already a mount root`);
    }
    const parentMount = this.owningMount(parentNode);
    const mount: Mount = {
      name: spec.name,
      path,
      rootId: spec.rootId,
      readOnly: spec.readOnly ?? false,
      parent: parentMount.name,
    };
    this.byName.set(mount.name, mount);
    this.byPath.set(mount.path, mount);
    this.byRoot.set(mount.rootId, mount);
    this.onChange();
    return mount;
  }

  /** Innermost mount whose root subtree contains the node. */
  owningMount(nodeId: string): Mount {
    let cur: string | null = nodeId;
    while (cur !== null) {
      const mount = this.byRoot.get(cur);
      if (mount) return mount;
      cur = this.tree.get(cur)?.parentId ?? null;
    }
    return this.rootMount;
  }

  /** Longest mount-path prefix on segment boundaries. */
  splitMount(segs: string[]): { mount: Mount; rest: string[] } {
    for (let k = segs.length; k >= 1; k--) {
      const mount = this.byPath.get(joinPath(segs.slice(0, k)));
      if (mount) return { mount, rest: segs.slice(k) };
    }
    return { mount: this.rootMount, rest: segs };
  }
}

function normalizeMountPath(path: string): string {
  const segs = splitPath(path);
  if (segs.length === 0) {
    throw new VfsError(ERR.InvalidPath, 'mount path cannot be the root');
  }
  for (const seg of segs) {
    if (!isValidName(seg)) {
      throw new VfsError(ERR.InvalidPath, `invalid mount path: ${path}`);
    }
  }
  return joinPath(segs);
}

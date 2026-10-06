import { NotFoundError, RootEscapeError, TreeError } from "./errors";
import { canonicalPath, splitPath } from "./pathutil";
import { Tree } from "./tree";

export interface Mount {
  readonly name: string;
  readonly rootId: string;
  readonly readOnly: boolean;
}

export interface MountResolution {
  mount: Mount;
  nodeId: string;
}

/**
 * An explicit graph of virtual mounts. Each mount owns an independent root
 * node and a read-only flag; mounts attach at a path inside a parent mount.
 * The graph may contain cycles: concrete paths are finite so walking them
 * always terminates, and '..' may never cross above a mount root.
 */
export class MountGraph {
  private readonly mounts = new Map<string, Mount>();
  private readonly attachPoints = new Map<string, string>();
  private readonly rootIds = new Map<string, string>();
  private rootMountName: string | null = null;

  constructor(
    private readonly tree: Tree,
    private readonly onChange: () => void = () => {},
  ) {}

  addMount(name: string, rootId: string, readOnly = false): Mount {
    if (this.mounts.has(name)) throw new TreeError(`mount already exists: ${name}`);
    if (!this.tree.has(rootId))
      throw new NotFoundError(`mount root does not exist: ${rootId}`);
    const mount: Mount = { name, rootId, readOnly };
    this.mounts.set(name, mount);
    this.rootIds.set(rootId, name);
    if (this.rootMountName === null) this.rootMountName = name;
    this.onChange();
    return mount;
  }

  /**
   * The mount that owns a node: the nearest ancestor-or-self that is a mount
   * root. Ownership is intrinsic to the subtree, so it cannot be bypassed by
   * reaching the node through a different path or alias.
   */
  mountOf(nodeId: string): Mount {
    let current: string | null = nodeId;
    while (current !== null) {
      const name = this.rootIds.get(current);
      if (name !== undefined) return this.get(name);
      current = this.tree.parentOf(current);
    }
    throw new TreeError(`node ${nodeId} is not under any mount root`);
  }

  get(name: string): Mount {
    const mount = this.mounts.get(name);
    if (!mount) throw new NotFoundError(`no such mount: ${name}`);
    return mount;
  }

  get rootMount(): Mount {
    if (this.rootMountName === null) throw new TreeError("no root mount configured");
    return this.get(this.rootMountName);
  }

  attach(childName: string, parentName: string, pathInParent: string): void {
    this.get(childName);
    this.get(parentName);
    const rel = canonicalPath(pathInParent);
    if (rel === "/") throw new TreeError("cannot attach at a mount root");
    const key = this.key(parentName, rel);
    if (this.attachPoints.has(key))
      throw new TreeError(`attach point already occupied: ${parentName}:${rel}`);
    this.attachPoints.set(key, childName);
    this.onChange();
  }

  resolve(path: string): MountResolution {
    let mount = this.rootMount;
    let nodeId = mount.rootId;
    let rel = "/";
    for (const segment of splitPath(path)) {
      if (segment === "..") {
        if (rel === "/")
          throw new RootEscapeError(`'..' escapes root of mount ${mount.name}`);
        const parent = this.tree.parentOf(nodeId);
        if (parent === null)
          throw new RootEscapeError(`'..' escapes root of mount ${mount.name}`);
        nodeId = parent;
        rel = rel.slice(0, rel.lastIndexOf("/")) || "/";
        continue;
      }
      const nextRel = rel === "/" ? "/" + segment : rel + "/" + segment;
      const childName = this.attachPoints.get(this.key(mount.name, nextRel));
      if (childName !== undefined) {
        mount = this.get(childName);
        nodeId = mount.rootId;
        rel = "/";
        continue;
      }
      const next = this.tree.childByName(nodeId, segment);
      if (next === null)
        throw new NotFoundError(`no node at ${path} (segment '${segment}')`);
      nodeId = next;
      rel = nextRel;
    }
    return { mount, nodeId };
  }

  private key(parentName: string, rel: string): string {
    return `${parentName}\n${rel}`;
  }
}

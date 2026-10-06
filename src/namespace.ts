import { AliasResolution, AliasTable } from "./alias";
import { ReadOnlyError } from "./errors";
import { Mount, MountGraph } from "./mount";
import { MoveTransaction } from "./transaction";
import { Tree } from "./tree";

export interface ResolvedPath {
  nodeId: string;
  realPath: string;
  mount: Mount;
  alias: AliasResolution;
}

/**
 * Composes the tree, alias table, and mount graph. Alias interpretation runs
 * first, then the resulting concrete path is walked through the mount graph.
 * Write access is checked against the mount that owns the *resolved* node,
 * so an alias can never smuggle a write across a read-only boundary.
 *
 * configVersion increments on every alias/mount configuration change and is
 * used by transactions to detect configuration switches after validation.
 */
export class Namespace {
  readonly tree = new Tree();
  readonly aliases: AliasTable;
  readonly mounts: MountGraph;
  private version = 0;

  constructor(maxAliasHops = 32) {
    const bump = () => {
      this.version++;
    };
    this.aliases = new AliasTable(this.tree, maxAliasHops, bump);
    this.mounts = new MountGraph(this.tree, bump);
  }

  get configVersion(): number {
    return this.version;
  }

  resolvePath(path: string, opts: { forWrite?: boolean } = {}): ResolvedPath {
    const alias = this.aliases.resolve(path);
    const { nodeId } = this.mounts.resolve(alias.path);
    const mount = this.mounts.mountOf(nodeId);
    if (opts.forWrite && mount.readOnly)
      throw new ReadOnlyError(
        `path ${path} resolves into read-only mount ${mount.name}`,
      );
    return { nodeId, realPath: this.tree.realPath(nodeId), mount, alias };
  }

  beginMove(sourcePath: string, targetDirPath: string): MoveTransaction {
    return new MoveTransaction(this, sourcePath, targetDirPath);
  }
}

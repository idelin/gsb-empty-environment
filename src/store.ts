import { VfsError, ERR } from './errors.ts';
import { TreeStore } from './tree.ts';
import { AliasTable } from './aliases.ts';
import { MountGraph } from './mounts.ts';
import type { Mount, MountSpec } from './mounts.ts';
import { Resolver } from './resolver.ts';
import type { Resolved } from './resolver.ts';
import { Transaction } from './txn.ts';

/**
 * Facade wiring the tree, alias table, mount graph and resolver together.
 * Every alias/mount mutation bumps a configuration version that
 * transactions snapshot when they begin.
 */
export class VfsStore {
  readonly tree = new TreeStore();
  readonly aliases: AliasTable;
  readonly mounts: MountGraph;
  readonly resolver: Resolver;
  private configVersion = 0;
  /** Fuel used when validating a new alias; generous to allow deep chains. */
  private readonly checkFuel = 64;

  constructor() {
    const bump = (): void => {
      this.configVersion += 1;
    };
    this.aliases = new AliasTable(bump);
    this.mounts = new MountGraph(this.tree, bump);
    this.resolver = new Resolver(this.tree, this.aliases, this.mounts);
  }

  get version(): number {
    return this.configVersion;
  }

  /**
   * Registers an alias after proving it resolvable with the bounded
   * interpreter: the target must exist and resolving the alias path must
   * terminate without cycles. On failure the table is rolled back.
   */
  setAlias(path: string, targetId: string): void {
    if (!this.tree.has(targetId)) {
      throw new VfsError(ERR.AliasTargetMissing, `alias target ${targetId} does not exist`);
    }
    this.aliases.set(path, targetId);
    try {
      this.resolver.resolve(path, this.checkFuel);
    } catch (err) {
      this.aliases.remove(path);
      if (err instanceof VfsError && err.code === ERR.AliasResolutionLimit) {
        throw new VfsError(ERR.AliasCycle, `alias ${path} creates a resolution cycle`);
      }
      throw err;
    }
  }

  addMount(spec: MountSpec): Mount {
    return this.mounts.add(spec, (path) => this.resolver.resolve(path).nodeId);
  }

  resolvePath(path: string, fuel?: number): Resolved {
    return this.resolver.resolve(path, fuel);
  }

  movePath(srcPath: string, dstPath: string): void {
    const tx = this.beginTx();
    tx.move(srcPath, dstPath);
    tx.commit();
  }

  beginTx(): Transaction {
    return new Transaction(this);
  }
}

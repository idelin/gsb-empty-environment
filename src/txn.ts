import { VfsError, ERR } from './errors.ts';
import { splitPath, joinPath, isValidName } from './pathutil.ts';
import type { VfsStore } from './store.ts';

interface StagedOp {
  check(): void;
  apply(): void;
}

/**
 * Transactional move. Source and destination identities are resolved first,
 * then validated; mutations are staged and only applied by commit(). Commit
 * re-validates every staged op and refuses to run if the alias/mount
 * configuration changed since the transaction began, so a failed
 * transaction never modifies the tree.
 */
export class Transaction {
  private store: VfsStore;
  private version: number;
  private ops: StagedOp[] = [];
  private closed = false;

  constructor(store: VfsStore) {
    this.store = store;
    this.version = store.version;
  }

  move(srcPath: string, dstPath: string): this {
    this.assertOpen();
    const src = this.store.resolver.resolve(srcPath);
    const dstSegs = splitPath(dstPath);
    const leaf = dstSegs[dstSegs.length - 1];
    if (leaf === undefined || !isValidName(leaf)) {
      throw new VfsError(ERR.InvalidPath, `invalid destination path: ${dstPath}`);
    }
    const dstParent = this.store.resolver.resolve(joinPath(dstSegs.slice(0, -1)));
    const srcId = src.nodeId;
    const dstParentId = dstParent.nodeId;
    const check = (): void => {
      if (srcId === this.store.tree.rootId) {
        throw new VfsError(ERR.InvalidPath, 'cannot move the root node');
      }
      if (!this.store.tree.has(srcId) || !this.store.tree.has(dstParentId)) {
        throw new VfsError(ERR.NotFound, 'move endpoint no longer exists');
      }
      const srcMount = this.store.mounts.owningMount(srcId);
      if (srcMount.readOnly) {
        throw new VfsError(ERR.ReadOnlyMount, `source is inside read-only mount ${srcMount.path}`);
      }
      const dstMount = this.store.mounts.owningMount(dstParentId);
      if (dstMount.readOnly) {
        throw new VfsError(ERR.ReadOnlyMount, `destination is inside read-only mount ${dstMount.path}`);
      }
      if (srcId === dstParentId || this.store.tree.isAncestor(srcId, dstParentId)) {
        throw new VfsError(ERR.WouldCycle, 'cannot move a node into its own subtree');
      }
      const existing = this.store.tree.childByName(dstParentId, leaf);
      if (existing !== undefined && existing !== srcId) {
        throw new VfsError(ERR.AlreadyExists, `destination already exists: ${dstPath}`);
      }
    };
    check();
    this.ops.push({
      check,
      apply: () => this.store.tree.move(srcId, dstParentId, leaf),
    });
    return this;
  }

  commit(): void {
    this.assertOpen();
    if (this.store.version !== this.version) {
      this.ops = [];
      this.closed = true;
      throw new VfsError(ERR.ConfigChanged, 'alias/mount configuration changed during the transaction');
    }
    for (const op of this.ops) op.check();
    for (const op of this.ops) op.apply();
    this.closed = true;
  }

  abort(): void {
    this.ops = [];
    this.closed = true;
  }

  private assertOpen(): void {
    if (this.closed) {
      throw new VfsError(ERR.TransactionClosed, 'transaction is already closed');
    }
  }
}

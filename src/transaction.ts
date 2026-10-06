import { MoveValidationError, StaleConfigError, TreeError } from "./errors";
import type { Namespace } from "./namespace";

/**
 * Transactional move. prepare() resolves the source and target identities
 * first, then validates the move; commit() applies it. Any failure before or
 * during commit leaves the tree untouched. If the alias/mount configuration
 * changes between prepare() and commit(), commit aborts with StaleConfigError.
 */
export class MoveTransaction {
  private sourceId: string | null = null;
  private targetDirId: string | null = null;
  private versionAtPrepare = -1;
  private state: "new" | "prepared" | "committed" = "new";

  constructor(
    private readonly ns: Namespace,
    private readonly sourcePath: string,
    private readonly targetDirPath: string,
  ) {}

  prepare(): this {
    if (this.state !== "new") throw new TreeError("transaction already prepared");
    const source = this.ns.resolvePath(this.sourcePath, { forWrite: true });
    const target = this.ns.resolvePath(this.targetDirPath, { forWrite: true });
    this.sourceId = source.nodeId;
    this.targetDirId = target.nodeId;

    const tree = this.ns.tree;
    if (this.sourceId === tree.rootId)
      throw new MoveValidationError("cannot move the root");
    if (this.sourceId === this.targetDirId || tree.isAncestor(this.sourceId, this.targetDirId))
      throw new MoveValidationError("cannot move a node into its own subtree");
    const name = tree.get(this.sourceId).name;
    const clash = tree.childByName(this.targetDirId, name);
    if (clash !== null && clash !== this.sourceId)
      throw new MoveValidationError(`name conflict at target: ${name}`);

    this.versionAtPrepare = this.ns.configVersion;
    this.state = "prepared";
    return this;
  }

  commit(): void {
    if (this.state !== "prepared") throw new TreeError("transaction is not prepared");
    if (this.ns.configVersion !== this.versionAtPrepare)
      throw new StaleConfigError(
        "alias/mount configuration changed after validation; aborted without changes",
      );
    this.ns.tree.moveNode(this.sourceId!, this.targetDirId!);
    this.state = "committed";
  }
}

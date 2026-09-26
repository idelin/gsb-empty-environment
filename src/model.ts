import type { ContainerNode, DocNode, FlatCluster, Level, TextNode } from "./types.ts";
import { isTextNode } from "./types.ts";
import { splitGraphemes } from "./grapheme.ts";

export class StaleLayoutError extends Error {
  constructor(kind: string, heldVersion: number, currentVersion: number) {
    super(
      `Stale ${kind}: created against layout version ${heldVersion}, current version is ${currentVersion}. Re-resolve against the new layout before use.`,
    );
    this.name = "StaleLayoutError";
  }
}

export class DocumentModel {
  readonly root: DocNode;
  version: number;
  private clusters: FlatCluster[] = [];
  private readonly nodeLevels = new Map<string, Level[]>();
  private readonly nodeClusters = new Map<string, string[]>();

  constructor(root: DocNode) {
    this.root = root;
    this.version = 0;
    this.rebuild();
  }

  private rebuild(): void {
    this.clusters = [];
    this.nodeLevels.clear();
    this.nodeClusters.clear();
    let seq = 0;
    const ensureId = (node: DocNode): string => {
      if (node.id === undefined) node.id = `n${seq++}`;
      return node.id;
    };
    const walk = (node: DocNode, inherited: Level): void => {
      const id = ensureId(node);
      const ownLevel = node.level ?? inherited;
      if (isTextNode(node)) {
        const clusters =
          node.clusters !== undefined ? [...node.clusters] : splitGraphemes(node.text ?? "");
        if (node.levels !== undefined && node.levels.length !== clusters.length) {
          throw new Error(
            `Text node ${id}: levels length ${node.levels.length} does not match cluster count ${clusters.length}`,
          );
        }
        const levels: Level[] = clusters.map((_, i) => node.levels?.[i] ?? ownLevel);
        this.nodeClusters.set(id, clusters);
        this.nodeLevels.set(id, levels);
        for (let i = 0; i < clusters.length; i++) {
          this.clusters.push({
            index: this.clusters.length,
            text: clusters[i]!,
            level: levels[i]!,
            nodeId: id,
          });
        }
      } else {
        for (const child of node.children) walk(child, ownLevel);
      }
    };
    walk(this.root, 0);
  }

  getClusters(): readonly FlatCluster[] {
    return this.clusters;
  }

  get length(): number {
    return this.clusters.length;
  }

  levelsOf(nodeId: string): readonly Level[] | undefined {
    return this.nodeLevels.get(nodeId);
  }

  clustersOf(nodeId: string): readonly string[] | undefined {
    return this.nodeClusters.get(nodeId);
  }

  /**
   * Local level update: replace levels of clusters [start, start+count) of one
   * text node. Returns a NEW model with a bumped version; carets/hits/
   * selections derived from the old model do not interoperate with the new one.
   */
  updateLevels(nodeId: string, start: number, count: number, level: Level): DocumentModel {
    const current = this.levelsOf(nodeId);
    if (current === undefined) throw new Error(`Unknown text node: ${nodeId}`);
    if (start < 0 || count < 0 || start + count > current.length) {
      throw new Error(`Level range [${start}, ${start + count}) out of bounds (${current.length})`);
    }
    const cloned = cloneTree(this.root);
    const next = new DocumentModel(cloned);
    next.version = this.version + 1;
    const target = next.findTextNode(nodeId);
    if (target === undefined) throw new Error(`Unknown text node after clone: ${nodeId}`);
    target.levels = current.map((existing, i) =>
      i >= start && i < start + count ? level : existing,
    );
    next.rebuild();
    return next;
  }

  private findTextNode(id: string, node: DocNode = this.root): TextNode | undefined {
    if (isTextNode(node)) return node.id === id ? node : undefined;
    for (const child of node.children) {
      const found = this.findTextNode(id, child);
      if (found !== undefined) return found;
    }
    return undefined;
  }
}

function cloneTree(node: DocNode): DocNode {
  if (isTextNode(node)) {
    return {
      ...node,
      levels: node.levels !== undefined ? [...node.levels] : undefined,
      clusters: node.clusters !== undefined ? [...node.clusters] : undefined,
    };
  }
  const container = node as ContainerNode;
  return { ...container, children: container.children.map(cloneTree) };
}

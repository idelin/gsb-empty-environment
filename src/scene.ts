import type { Mat2D } from "./matrix.ts";
import { SceneNode } from "./node.ts";

export interface SnapshotEntry {
  node: SceneNode;
  matrix: Mat2D;
  version: number;
  parent: SceneNode | null;
  children: SceneNode[];
}

/**
 * An immutable capture of a scene's transform chains and topology.
 * Nodes mutated after the snapshot keep their old matrices here, so hit
 * testing through a snapshot always follows the old transform chain.
 */
export class SceneSnapshot {
  private readonly entries = new Map<number, SnapshotEntry>();

  constructor(root: SceneNode) {
    const walk = (node: SceneNode): void => {
      this.entries.set(node.id, {
        node,
        matrix: { ...node.matrix },
        version: node.version,
        parent: node.parent,
        children: [...node.children],
      });
      for (const child of node.children) walk(child);
    };
    walk(root);
  }

  entry(node: SceneNode): SnapshotEntry | undefined {
    return this.entries.get(node.id);
  }

  has(node: SceneNode): boolean {
    return this.entries.has(node.id);
  }
}

export class Scene {
  readonly root: SceneNode;

  constructor(root: SceneNode) {
    this.root = root;
  }

  snapshot(): SceneSnapshot {
    return new SceneSnapshot(this.root);
  }
}

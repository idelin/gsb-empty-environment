import { hash32 } from './hash.ts';
import { RedBlackTree } from './redBlackTree.ts';

export interface RingMember {
  id: string;
  weight?: number;
}

export class HashRing<T extends RingMember> {
  private readonly vnodesPerWeight: number;
  private tree = new RedBlackTree<string>();
  private readonly members = new Map<string, T>();

  constructor(vnodesPerWeight = 160) {
    this.vnodesPerWeight = vnodesPerWeight;
  }

  get size(): number {
    return this.members.size;
  }

  has(id: string): boolean {
    return this.members.has(id);
  }

  ids(): string[] {
    return [...this.members.keys()];
  }

  add(member: T): void {
    this.members.set(member.id, member);
    this.rebuild();
  }

  remove(id: string): void {
    if (this.members.delete(id)) {
      this.rebuild();
    }
  }

  lookup(key: string, excluded?: ReadonlySet<string>): string | null {
    if (this.tree.size === 0) return null;
    let point = hash32(key);
    for (let probe = 0; probe < this.tree.size; probe += 1) {
      const entry = this.tree.ceiling(point) ?? this.tree.minimum();
      if (entry === null) return null;
      if (excluded === undefined || !excluded.has(entry.value)) {
        return entry.value;
      }
      point = (entry.key + 1) >>> 0;
    }
    return null;
  }

  private rebuild(): void {
    this.tree = new RedBlackTree<string>();
    for (const member of this.members.values()) {
      const weight = Math.max(1, Math.floor(member.weight ?? 1));
      const vnodes = weight * this.vnodesPerWeight;
      for (let i = 0; i < vnodes; i += 1) {
        let point = hash32(`${member.id}#${i}`);
        while (this.tree.ceiling(point)?.key === point) {
          point = (point + 1) >>> 0;
        }
        this.tree.insert(point, member.id);
      }
    }
  }
}

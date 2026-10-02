import { sha256Hex } from "./hash.ts";
import type { StateRecord } from "./state.ts";

/** Tree depth in hex nibbles: each level branches 16 ways. */
export const TREE_DEPTH = 4;
export const BRANCHING = 16;

const EMPTY_CHILD = "-";
const EMPTY_SUBTREE_HASH = sha256Hex("empty-subtree");

function serializeRecord(record: StateRecord): string {
  return `${record.nodeId}:${record.status}:${record.incarnation}:${record.updatedAt}`;
}

/**
 * Merkle tree over the membership table.
 *
 * Each key (nodeId) is hashed with SHA-256; the first TREE_DEPTH hex nibbles
 * of the digest locate the leaf bucket. Internal nodes store the hash of
 * their 16 children, so two replicas can locate diverging keys by comparing
 * hashes level by level instead of scanning the full table.
 */
export class MerkleTree {
  readonly depth: number;
  private nodeHashes = new Map<string, string>();

  constructor(records: StateRecord[], depth: number = TREE_DEPTH) {
    this.depth = depth;
    this.build(records);
  }

  static keyOf(nodeId: string): string {
    return sha256Hex(nodeId);
  }

  static prefixOf(nodeId: string, depth: number = TREE_DEPTH): string {
    return MerkleTree.keyOf(nodeId).slice(0, depth);
  }

  /** Hash of the subtree at `prefix`; "" is the root. */
  hashAt(prefix: string): string {
    return this.nodeHashes.get(prefix) ?? EMPTY_SUBTREE_HASH;
  }

  private build(records: StateRecord[]): void {
    this.nodeHashes.clear();
    const buckets = new Map<string, StateRecord[]>();
    for (const record of records) {
      const prefix = MerkleTree.prefixOf(record.nodeId, this.depth);
      const bucket = buckets.get(prefix);
      if (bucket) bucket.push(record);
      else buckets.set(prefix, [record]);
    }

    const compute = (prefix: string): string | null => {
      if (prefix.length === this.depth) {
        const bucket = buckets.get(prefix);
        if (!bucket) return null;
        const payload = bucket.map(serializeRecord).sort().join("|");
        const hash = sha256Hex(`leaf|${payload}`);
        this.nodeHashes.set(prefix, hash);
        return hash;
      }
      let childPayload = "";
      let hasChildren = false;
      for (let i = 0; i < BRANCHING; i++) {
        const childHash = compute(prefix + i.toString(16));
        childPayload += childHash ?? EMPTY_CHILD;
        if (childHash !== null) hasChildren = true;
      }
      if (!hasChildren && prefix.length > 0) return null;
      const hash = sha256Hex(`node|${prefix.length}|${childPayload}`);
      this.nodeHashes.set(prefix, hash);
      return hash;
    };

    compute("");
  }
}

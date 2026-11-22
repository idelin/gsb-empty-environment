import { hashPair, hashValue, sha256Hex } from "./canonical.ts";
import { ledgerError } from "./errors.ts";
import { authorizationLeafHash, stateLeafHash, type InternalAuthorizationState } from "./reducer.ts";
import type { MerkleProof, MerkleProofNode } from "./merkle-types.ts";
import type { AuthorizationTree } from "./types.ts";

export const MERKLE_DEPTH = 32;
export const EMPTY_LEAF = hashValue("wallet-merkle-empty-leaf-v1");

function buildEmptyNodes(): ReadonlyArray<string> {
  const nodes: string[] = new Array(MERKLE_DEPTH + 1);
  nodes[0] = EMPTY_LEAF;
  for (let height = 1; height <= MERKLE_DEPTH; height += 1) {
    nodes[height] = hashPair(nodes[height - 1], nodes[height - 1]);
  }
  return nodes;
}

const EMPTY_NODES = buildEmptyNodes();
export const EMPTY_ROOT = EMPTY_NODES[MERKLE_DEPTH];

export interface SparseLeaf {
  readonly key: string;
  readonly leaf: string;
  readonly path: string;
  readonly bits: string;
}

export interface SparseTreeEntry {
  readonly key: string;
  readonly leaf: string;
}

export interface SparseTree {
  readonly root: string;
  readonly leaves: ReadonlyMap<string, SparseLeaf>;
}

interface BuiltSparseTree extends SparseTree {
  readonly nodeAt: (prefix: string, length: number) => string;
}

export function merklePath(key: string): string {
  return sha256Hex(`wallet-merkle-path-v1|${key}`).slice(2, 2 + MERKLE_DEPTH / 4);
}

export function merklePathBits(key: string): string {
  return pathBits(merklePath(key));
}

function pathBits(path: string): string {
  let bits = "";
  for (const nibble of path) {
    const value = Number.parseInt(nibble, 16);
    bits += value.toString(2).padStart(4, "0");
  }
  return bits.slice(0, MERKLE_DEPTH);
}

function flipLastBit(bits: string): string {
  const prefix = bits.slice(0, -1);
  return `${prefix}${bits.endsWith("0") ? "1" : "0"}`;
}

export function buildSparseTree(entries: Iterable<SparseTreeEntry>): SparseTree {
  return buildSparseTreeInternal(entries);
}

function buildSparseTreeInternal(entries: Iterable<SparseTreeEntry>): BuiltSparseTree {
  const leaves = new Map<string, SparseLeaf>();
  const byPath = new Map<string, SparseLeaf>();

  for (const entry of entries) {
    if (leaves.has(entry.key)) {
      throw ledgerError("INVALID_PROOF", `duplicate Merkle leaf key ${entry.key}`);
    }
    const path = merklePath(entry.key);
    const bits = pathBits(path);
    const collision = byPath.get(bits);
    if (collision) {
      throw ledgerError(
        "INVALID_PROOF",
        `Merkle path collision between ${collision.key} and ${entry.key}`,
      );
    }
    const leaf = Object.freeze({
      key: entry.key,
      leaf: entry.leaf,
      path,
      bits,
    });
    leaves.set(entry.key, leaf);
    byPath.set(bits, leaf);
  }

  const nodeCache = new Map<string, string>();
  const nodeAt = (prefix: string, length: number): string => {
    const cacheKey = `${length}:${prefix}`;
    const cached = nodeCache.get(cacheKey);
    if (cached !== undefined) return cached;

    let containsLeaf = false;
    for (const leaf of byPath.values()) {
      if (leaf.bits.startsWith(prefix)) {
        containsLeaf = true;
        break;
      }
    }
    if (!containsLeaf) {
      const empty = EMPTY_NODES[MERKLE_DEPTH - length] ?? EMPTY_LEAF;
      nodeCache.set(cacheKey, empty);
      return empty;
    }
    if (length === MERKLE_DEPTH) {
      const leaf = byPath.get(prefix);
      if (!leaf) throw ledgerError("INVALID_PROOF", "non-empty subtree has no leaf");
      nodeCache.set(cacheKey, leaf.leaf);
      return leaf.leaf;
    }

    const value = hashPair(nodeAt(`${prefix}0`, length + 1), nodeAt(`${prefix}1`, length + 1));
    nodeCache.set(cacheKey, value);
    return value;
  };

  return Object.freeze({
    root: nodeAt("", 0),
    leaves,
    nodeAt,
  });
}

function siblingFor(
  tree: BuiltSparseTree,
  bits: string,
  level: number,
): MerkleProofNode {
  const prefixLength = MERKLE_DEPTH - level;
  const currentPrefix = bits.slice(0, prefixLength);
  const siblingPrefix = flipLastBit(currentPrefix);
  const hash = tree.nodeAt(siblingPrefix, prefixLength);
  return Object.freeze({
    level,
    hash,
    position: currentPrefix.endsWith("0") ? "right" : "left",
  });
}

export function createMerkleProof(
  entries: Iterable<SparseTreeEntry>,
  key: string,
): MerkleProof {
  const tree = buildSparseTreeInternal(entries);
  const leaf = tree.leaves.get(key);
  if (!leaf) {
    throw ledgerError("INVALID_PROOF", `Merkle leaf ${key} is not in the tree`);
  }
  const bits = pathBits(leaf.path);
  const siblings: MerkleProofNode[] = [];
  for (let level = 0; level < MERKLE_DEPTH; level += 1) {
    siblings.push(siblingFor(tree, bits, level));
  }
  return Object.freeze({
    key,
    leaf: leaf.leaf,
    root: tree.root,
    depth: MERKLE_DEPTH,
    path: leaf.path,
    siblings: Object.freeze(siblings),
  });
}

export function proofForAbsentKey(
  entries: Iterable<SparseTreeEntry>,
  key: string,
): MerkleProof {
  const tree = buildSparseTreeInternal(entries);
  const path = merklePath(key);
  if (tree.leaves.has(key)) {
    throw ledgerError("INVALID_PROOF", `Merkle leaf ${key} is already present`);
  }
  for (const leaf of tree.leaves.values()) {
    if (leaf.bits === pathBits(path)) {
      throw ledgerError(
        "INVALID_PROOF",
        `cannot insert ${key}: its Merkle path collides with ${leaf.key}`,
      );
    }
  }
  const bits = pathBits(path);
  const siblings: MerkleProofNode[] = [];
  for (let level = 0; level < MERKLE_DEPTH; level += 1) {
    siblings.push(siblingFor(tree, bits, level));
  }
  return Object.freeze({
    key,
    leaf: EMPTY_LEAF,
    root: tree.root,
    depth: MERKLE_DEPTH,
    path,
    siblings: Object.freeze(siblings),
  });
}

export function verifyMerkleProof(
  proof: MerkleProof,
  expectedRoot?: string,
  expectedLeaf?: string,
): boolean {
  try {
    assertMerkleProof(proof, expectedRoot, expectedLeaf);
    return true;
  } catch {
    return false;
  }
}

export function assertMerkleProof(
  proof: MerkleProof,
  expectedRoot?: string,
  expectedLeaf?: string,
): void {
  if (!proof || typeof proof !== "object") {
    throw ledgerError("INVALID_PROOF", "Merkle proof must be an object");
  }
  if (proof.depth !== MERKLE_DEPTH) {
    throw ledgerError(
      "INVALID_PROOF",
      `Merkle proof depth must be ${MERKLE_DEPTH}, got ${String(proof.depth)}`,
    );
  }
  const expectedPath = merklePath(proof.key);
  if (proof.path !== expectedPath) {
    throw ledgerError("INVALID_PROOF", "Merkle proof path is not bound to its key");
  }
  if (expectedLeaf !== undefined && proof.leaf !== expectedLeaf) {
    throw ledgerError("INVALID_PROOF", "Merkle proof leaf does not match the expected leaf");
  }
  if (!Array.isArray(proof.siblings) || proof.siblings.length !== MERKLE_DEPTH) {
    throw ledgerError(
      "INVALID_PROOF",
      `Merkle proof has a missing sibling: expected ${MERKLE_DEPTH} levels`,
    );
  }

  const bits = pathBits(proof.path);
  const levels = new Set<number>();
  let node = proof.leaf;
  for (const sibling of proof.siblings) {
    if (
      !sibling ||
      !Number.isInteger(sibling.level) ||
      sibling.level < 0 ||
      sibling.level >= MERKLE_DEPTH
    ) {
      throw ledgerError("INVALID_PROOF", "Merkle proof contains an invalid sibling level");
    }
    if (levels.has(sibling.level)) {
      throw ledgerError(
        "INVALID_PROOF",
        `Merkle proof contains two roots at height ${sibling.level}`,
      );
    }
    levels.add(sibling.level);
    if (typeof sibling.hash !== "string" || sibling.hash === "") {
      throw ledgerError("INVALID_PROOF", "Merkle proof sibling hash is missing");
    }

    const prefixLength = MERKLE_DEPTH - sibling.level;
    const currentPrefix = bits.slice(0, prefixLength);
    const expectedPosition = currentPrefix.endsWith("0") ? "right" : "left";
    if (sibling.position !== expectedPosition) {
      throw ledgerError(
        "INVALID_PROOF",
        `Merkle proof sibling at height ${sibling.level} is on the wrong side`,
      );
    }

    node =
      expectedPosition === "right"
        ? hashPair(node, sibling.hash)
        : hashPair(sibling.hash, node);
  }

  for (let level = 0; level < MERKLE_DEPTH; level += 1) {
    if (!levels.has(level)) {
      throw ledgerError("INVALID_PROOF", `Merkle proof is missing sibling at height ${level}`);
    }
  }

  if (node !== proof.root) {
    throw ledgerError("INVALID_PROOF", "Merkle proof does not reconstruct its root");
  }
  if (expectedRoot !== undefined && node !== expectedRoot) {
    throw ledgerError("INVALID_PROOF", "Merkle proof root does not match the committed root");
  }
}

export function buildAuthorizationTree(
  states: Iterable<InternalAuthorizationState>,
): AuthorizationTree {
  const entries: SparseTreeEntry[] = [];
  for (const state of states) {
    entries.push({ key: state.key, leaf: stateLeafHash(state) });
  }
  const tree = buildSparseTree(entries);
  return Object.freeze({
    root: tree.root,
    leaves: new Map(Array.from(tree.leaves, ([key, leaf]) => [key, leaf.leaf])),
  });
}

export function authorizationTreeEntries(
  states: Iterable<InternalAuthorizationState>,
): SparseTreeEntry[] {
  return Array.from(states, (state) => ({ key: state.key, leaf: stateLeafHash(state) }));
}

export function authorizationLeafForState(state: InternalAuthorizationState): string {
  return authorizationLeafHash(state);
}

export { EMPTY_NODES };

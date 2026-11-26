/**
 * 键排序的仅追加 Merkle 树。
 *
 * - 叶子与内部节点使用域分离前缀，避免二阶碰撞。
 * - 缺失证明给出键序相邻叶子与位标记，验证者可独立确认插入位置为空。
 * - 实现零依赖，哈希采用宿主注入（默认 SHA-256），便于核验组件复用。
 */

export type HashFn = (data: Uint8Array) => Uint8Array;

const LEAF_PREFIX = 0x00;
const INTERNAL_PREFIX = 0x01;

function concat(parts: Uint8Array[]): Uint8Array {
  const total = parts.reduce((sum, part) => sum + part.length, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const part of parts) {
    out.set(part, offset);
    offset += part.length;
  }
  return out;
}

function encodeBytes(value: string): Uint8Array {
  return new TextEncoder().encode(value);
}

export function leafHash(key: string, value: string, hash: HashFn): Uint8Array {
  const keyBytes = encodeBytes(key);
  const valueBytes = encodeBytes(value);
  const lengthPrefix = new Uint8Array(8);
  new DataView(lengthPrefix.buffer).setUint32(0, keyBytes.length);
  new DataView(lengthPrefix.buffer).setUint32(4, valueBytes.length);
  return hash(concat([new Uint8Array([LEAF_PREFIX]), lengthPrefix, keyBytes, valueBytes]));
}

function internalHash(left: Uint8Array, right: Uint8Array, hash: HashFn): Uint8Array {
  return hash(concat([new Uint8Array([INTERNAL_PREFIX]), left, right]));
}

export interface MerkleTree {
  readonly root: Uint8Array;
  /** 升序键。 */
  readonly keys: readonly string[];
}

export interface InclusionProof {
  readonly kind: 'inclusion';
  readonly key: string;
  readonly value: string;
  /** 自叶子向上，兄弟节点哈希（hex）。 */
  readonly siblings: string[];
  /** 每层当前节点是否为右孩子。 */
  readonly indexBits: number[];
}

export interface ExclusionProof {
  readonly kind: 'exclusion';
  readonly key: string;
  /** 插入位置（键序）。 */
  readonly insertIndex: number;
  /** 树的叶子总数，用于校验末尾插入位。 */
  readonly leafCount: number;
  /** 前邻居包含证明（insertIndex>0 时存在）。 */
  readonly predecessor: InclusionProof | null;
  /** 后邻居包含证明（insertIndex<叶子数 时存在）。 */
  readonly successor: InclusionProof | null;
}

export type MerkleProof = InclusionProof | ExclusionProof;

function toHex(bytes: Uint8Array): string {
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
}

function fromHex(hex: string): Uint8Array {
  if (hex.length % 2 !== 0 || /[^0-9a-fA-F]/.test(hex)) {
    throw new Error(`invalid hex digest: ${hex}`);
  }
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i += 1) {
    out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  }
  return out;
}

/** 由键值对构建 Merkle 树（内部按键去重排序）。 */
export function buildMerkleTree(entries: ReadonlyArray<readonly [string, string]>, hash: HashFn): MerkleTree {
  const sorted = new Map(entries);
  const keys = Array.from(sorted.keys()).sort();
  if (keys.length === 0) {
    return { root: hash(new Uint8Array(0)), keys };
  }
  let level = keys.map((key) => leafHash(key, sorted.get(key)!, hash));
  while (level.length > 1) {
    const next: Uint8Array[] = [];
    for (let i = 0; i < level.length; i += 2) {
      const left = level[i];
      const right = level[i + 1] ?? left;
      next.push(internalHash(left, right, hash));
    }
    level = next;
  }
  return { root: level[0], keys };
}

function rootHex(tree: MerkleTree): string {
  return toHex(tree.root);
}

/** 生成包含证明（键不存在时抛错）。 */
export function proveInclusion(tree: MerkleTree, valueByKey: ReadonlyMap<string, string>, key: string, hash: HashFn): InclusionProof {
  const index = tree.keys.indexOf(key);
  if (index < 0) {
    throw new Error(`cannot prove inclusion: key not present: ${key}`);
  }
  const value = valueByKey.get(key);
  if (value === undefined) {
    throw new Error(`missing value for key: ${key}`);
  }
  const siblings: string[] = [];
  const indexBits: number[] = [];
  let level = tree.keys.map((leafKey) => leafHash(leafKey, valueByKey.get(leafKey)!, hash));
  let cursor = index;
  while (level.length > 1) {
    const isRight = cursor % 2 === 1;
    const siblingIndex = isRight ? cursor - 1 : cursor + 1;
    indexBits.push(isRight ? 1 : 0);
    if (siblingIndex < level.length) {
      siblings.push(toHex(level[siblingIndex]));
    } else {
      // 奇数层最后节点与自身配对，兄弟即自身。
      siblings.push(toHex(level[cursor]));
    }
    const next: Uint8Array[] = [];
    for (let i = 0; i < level.length; i += 2) {
      const left = level[i];
      const right = level[i + 1] ?? left;
      next.push(internalHash(left, right, hash));
    }
    level = next;
    cursor = Math.floor(cursor / 2);
  }
  return { kind: 'inclusion', key, value, siblings, indexBits };
}

/** 生成缺失证明。 */
export function proveExclusion(
  tree: MerkleTree,
  valueByKey: ReadonlyMap<string, string>,
  key: string,
  hash: HashFn,
): ExclusionProof {
  if (tree.keys.includes(key)) {
    throw new Error(`cannot prove exclusion: key present: ${key}`);
  }
  let insertIndex = 0;
  while (insertIndex < tree.keys.length && tree.keys[insertIndex] < key) {
    insertIndex += 1;
  }
  const predecessor =
    insertIndex > 0 ? proveInclusion(tree, valueByKey, tree.keys[insertIndex - 1], hash) : null;
  const successor =
    insertIndex < tree.keys.length ? proveInclusion(tree, valueByKey, tree.keys[insertIndex], hash) : null;
  return { kind: 'exclusion', key, insertIndex, leafCount: tree.keys.length, predecessor, successor };
}

function verifyInclusionProof(proof: InclusionProof, expectedRoot: Uint8Array, hash: HashFn): boolean {
  if (proof.siblings.length !== proof.indexBits.length) {
    return false;
  }
  let node = leafHash(proof.key, proof.value, hash);
  for (let depth = 0; depth < proof.siblings.length; depth += 1) {
    let sibling: Uint8Array;
    try {
      sibling = fromHex(proof.siblings[depth]);
    } catch {
      return false;
    }
    node = proof.indexBits[depth] === 1 ? internalHash(sibling, node, hash) : internalHash(node, sibling, hash);
  }
  return toHex(node) === toHex(expectedRoot);
}

/**
 * 独立验证 Merkle 证明。
 * 缺失证明额外校验：邻居键序严格夹住被声明缺失的键。
 */
export function verifyMerkleProof(
  proof: MerkleProof,
  expectedRoot: Uint8Array,
  hash: HashFn,
): boolean {
  if (proof.kind === 'inclusion') {
    return verifyInclusionProof(proof, expectedRoot, hash);
  }
  if (proof.insertIndex < 0) {
    return false;
  }
  if (proof.leafCount < 0 || proof.insertIndex > proof.leafCount) {
    return false;
  }
  if (proof.predecessor === null && proof.successor === null) {
    // 空树：根为空哈希。
    return proof.leafCount === 0 && toHex(expectedRoot) === toHex(hash(new Uint8Array(0)));
  }
  if (proof.predecessor) {
    if (!(proof.predecessor.key < proof.key)) {
      return false;
    }
    if (!verifyInclusionProof(proof.predecessor, expectedRoot, hash)) {
      return false;
    }
  }
  if (proof.successor) {
    if (!(proof.successor.key > proof.key)) {
      return false;
    }
    if (!verifyInclusionProof(proof.successor, expectedRoot, hash)) {
      return false;
    }
  }
  // 插入位必须与键序邻居严格一致，防止位序伪造。
  if (proof.predecessor && proof.successor) {
    if (!(proof.predecessor.key < proof.key && proof.key < proof.successor.key)) {
      return false;
    }
  }
  if (proof.predecessor === null && proof.successor) {
    if (!(proof.insertIndex === 0 && proof.leafCount >= 1)) {
      return false;
    }
  }
  if (proof.successor === null && proof.predecessor) {
    if (proof.insertIndex !== proof.leafCount) {
      return false;
    }
  }
  if (proof.predecessor && proof.successor && proof.leafCount < 2) {
    return false;
  }
  // 必须确有相邻边界：两端都缺时只允许空树（已在上面处理）。
  if (proof.predecessor === null && proof.successor === null) {
    return false;
  }
  return true;
}

export { toHex, fromHex, rootHex };

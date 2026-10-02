import { generateKeyPairSync, sign, verify, createPublicKey, type KeyObject } from 'node:crypto';
import { canonicalJson, hashEraseMarker, hashLeaf, randomId, u64 } from './crypto.ts';
import { MerkleTree, type MerkleProof } from './merkle.ts';

export interface CustomerRecord {
  customerId: string;
  [field: string]: unknown;
}

export interface EraseReceipt {
  treeId: string;
  leafIndex: number;
  customerId: string;
  erasedAt: string;
  originalLeafHash: string;
  eraseMarkerHash: string;
  deletionSignature: string;
  officerPublicKey: string;
  rootBefore: string;
  rootAfter: string;
  treeSize: number;
  /** 被擦除节点从叶到根的完整哈希链（levelHashes[0]=标记哈希，末位=rootAfter），供旧存证本地演进。 */
  levelHashes: string[];
  merkleProof: {
    leafIndex: number;
    leafHash: string;
    path: { position: 'left' | 'right'; hash: string }[];
  };
}

export interface InclusionReceipt {
  treeId: string;
  treeSize: number;
  leafIndex: number;
  customerId: string;
  leafHash: string;
  erased: boolean;
  path: { position: 'left' | 'right'; hash: string }[];
}

interface Slot {
  customerId: string;
  leafHash: Buffer;
  erased: boolean;
  eraseMarkerHash?: Buffer;
  deletionSignature?: Buffer;
  erasedAt?: string;
}

const ERASE_INTENT = 'GDPR-ERASE/v1';

/** 合规官密钥对：删除指令必须携带其 Ed25519 签名。 */
export function generateOfficerKeyPair(): { publicKey: KeyObject; privateKey: KeyObject } {
  return generateKeyPairSync('ed25519');
}

function exportPublicKeyPem(key: KeyObject): string {
  return key.export({ type: 'spki', format: 'pem' }).toString();
}

function importPublicKeyPem(pem: string): KeyObject {
  return createPublicKey(pem);
}

/** 删除意图报文：绑定树、叶子、原叶哈希与时间，防跨树/跨叶重放。 */
export function deletionMessage(treeId: string, leafIndex: number, originalLeafHash: Buffer, erasedAt: string): Buffer {
  return Buffer.concat([
    Buffer.from(ERASE_INTENT, 'utf8'),
    Buffer.from(treeId, 'utf8'),
    u64(leafIndex),
    originalLeafHash,
    Buffer.from(erasedAt, 'utf8'),
  ]);
}

/**
 * 可擦除合规数据脱敏证明器。
 *
 * - 注册：记录明文仅存于易失存储，树上只留 H(记录) 叶子。
 * - 擦除：叶子被重写为 EraseMarker = H(erase | idx | 原叶哈希 | 删除签名)，
 *   明文 Buffer 原地清零并销毁引用（物理擦除），树根平滑演进。
 * - 存证：其余客户的 Merkle 路径在新根下依然完全有效。
 */
export class ErasableComplianceTree {
  readonly treeId: string;
  private readonly officerPublicKey: KeyObject;
  private readonly officerPrivateKey?: KeyObject;
  private slots: Slot[] = [];
  private plaintext: (Buffer | null)[] = [];
  private tree: MerkleTree | null = null;
  private rootHistory: Buffer[] = [];

  constructor(opts: { treeId?: string; officerPublicKey: KeyObject; officerPrivateKey?: KeyObject }) {
    this.treeId = opts.treeId ?? randomId();
    this.officerPublicKey = opts.officerPublicKey;
    this.officerPrivateKey = opts.officerPrivateKey;
  }

  get size(): number {
    return this.slots.length;
  }

  /** 当前树根；空树返回 null。 */
  root(): Buffer | null {
    return this.tree ? this.tree.root() : null;
  }

  rootHex(): string | null {
    const r = this.root();
    return r ? r.toString('hex') : null;
  }

  /** 根的演进历史（含每次擦除后的新根）。 */
  roots(): string[] {
    return this.rootHistory.map((r) => r.toString('hex'));
  }

  /** 注册客户记录，返回叶子下标。 */
  register(record: CustomerRecord): number {
    const payload = Buffer.from(canonicalJson(record), 'utf8');
    const leafHash = hashLeaf(payload);
    this.slots.push({ customerId: record.customerId, leafHash, erased: false });
    this.plaintext.push(payload);
    this.rebuild();
    return this.slots.length - 1;
  }

  /** 读取明文（仅未擦除时可用，模拟受控解密通道）。 */
  readRecord(leafIndex: number): CustomerRecord {
    const buf = this.plaintext[this.assertSlot(leafIndex)];
    if (buf === null) throw new Error(`记录 #${leafIndex} 已被物理擦除，明文不存在`);
    return JSON.parse(buf.toString('utf8')) as CustomerRecord;
  }

  isErased(leafIndex: number): boolean {
    return this.slots[this.assertSlot(leafIndex)].erased;
  }

  /**
   * 响应“被遗忘权”删除请求：
   * 1. 合规官私钥对删除意图签名；
   * 2. 叶子重写为 EraseMarker，根平滑演进；
   * 3. 明文 Buffer 原地清零并置空（物理擦除）。
   */
  erase(leafIndex: number, erasedAt: string = new Date().toISOString()): EraseReceipt {
    const idx = this.assertSlot(leafIndex);
    const slot = this.slots[idx];
    if (slot.erased) throw new Error(`记录 #${idx} 已处于擦除状态`);
    if (!this.officerPrivateKey) throw new Error('本实例未持有合规官私钥，无法签发删除');

    const rootBefore = this.root()!;
    const message = deletionMessage(this.treeId, idx, slot.leafHash, erasedAt);
    const signature = sign(null, message, this.officerPrivateKey);
    const marker = hashEraseMarker(idx, slot.leafHash, signature);

    // 物理擦除：先原地清零字节，再切断引用
    const buf = this.plaintext[idx];
    if (buf !== null) {
      buf.fill(0);
      this.plaintext[idx] = null;
    }

    slot.erased = true;
    slot.eraseMarkerHash = marker;
    slot.deletionSignature = signature;
    slot.erasedAt = erasedAt;
    this.tree!.updateLeaf(idx, marker);
    const rootAfter = this.tree!.root();
    this.rootHistory.push(Buffer.from(rootAfter));

    const proof = this.tree!.prove(idx);
    const levelHashes = this.levelHashChain(idx);
    return {
      treeId: this.treeId,
      treeSize: this.slots.length,
      leafIndex: idx,
      customerId: slot.customerId,
      erasedAt,
      originalLeafHash: slot.leafHash.toString('hex'),
      eraseMarkerHash: marker.toString('hex'),
      deletionSignature: signature.toString('hex'),
      officerPublicKey: exportPublicKeyPem(this.officerPublicKey),
      rootBefore: rootBefore.toString('hex'),
      rootAfter: rootAfter.toString('hex'),
      treeSize: this.slots.length,
      levelHashes: levelHashes.map((h) => h.toString('hex')),
      merkleProof: serializeProof(proof),
    };
  }

  /** 为未擦除客户出具存证路径。 */
  proveInclusion(leafIndex: number): InclusionReceipt {
    const idx = this.assertSlot(leafIndex);
    const slot = this.slots[idx];
    const proof = this.tree!.prove(idx);
    const levelHashes = this.levelHashChain(idx);
    return {
      treeId: this.treeId,
      treeSize: this.slots.length,
      leafIndex: idx,
      customerId: slot.customerId,
      leafHash: proof.leafHash.toString('hex'),
      erased: slot.erased,
      path: proof.path.map((s) => ({ position: s.position, hash: s.hash.toString('hex') })),
    };
  }

  /** 静态验证：客户凭 (记录明文, 存证路径, 当前根) 独立核验。 */
  static verifyInclusion(record: CustomerRecord, receipt: InclusionReceipt, expectedRootHex: string): boolean {
    if (receipt.erased) return false;
    const leafHash = hashLeaf(Buffer.from(canonicalJson(record), 'utf8'));
    if (leafHash.toString('hex') !== receipt.leafHash) return false;
    const proof: MerkleProof = {
      leafIndex: receipt.leafIndex,
      leafHash,
      path: receipt.path.map((s) => ({ position: s.position, hash: Buffer.from(s.hash, 'hex') })),
    };
    return MerkleTree.verify(proof, Buffer.from(expectedRootHex, 'hex'));
  }

  /** 静态验证：审计方核验擦除证明（签名有效 + 标记在根上）。 */
  static verifyErase(receipt: EraseReceipt, expectedRootHex?: string): boolean {
    const originalLeafHash = Buffer.from(receipt.originalLeafHash, 'hex');
    const signature = Buffer.from(receipt.deletionSignature, 'hex');
    const message = deletionMessage(receipt.treeId, receipt.leafIndex, originalLeafHash, receipt.erasedAt);
    let key: KeyObject;
    try {
      key = importPublicKeyPem(receipt.officerPublicKey);
    } catch {
      return false;
    }
    if (!verify(null, message, key, signature)) return false;

    const marker = hashEraseMarker(receipt.leafIndex, originalLeafHash, signature);
    if (marker.toString('hex') !== receipt.eraseMarkerHash) return false;

    // 哈希链端点一致性：链首为标记哈希，链尾为新根
    if (receipt.levelHashes.length === 0) return false;
    if (receipt.levelHashes[0] !== receipt.eraseMarkerHash) return false;
    if (receipt.levelHashes[receipt.levelHashes.length - 1] !== receipt.rootAfter) return false;

    const proof: MerkleProof = {
      leafIndex: receipt.merkleProof.leafIndex,
      leafHash: marker,
      path: receipt.merkleProof.path.map((s) => ({ position: s.position, hash: Buffer.from(s.hash, 'hex') })),
    };
    const root = expectedRootHex ?? receipt.rootAfter;
    return MerkleTree.verify(proof, Buffer.from(root, 'hex'));
  }

  /** 被擦除节点从叶到根的逐层哈希链（含根）。 */
  private levelHashChain(leafIndex: number): Buffer[] {
    const chain: Buffer[] = [];
    let level = this.slots.map((s) => (s.erased ? s.eraseMarkerHash! : s.leafHash));
    let idx = leafIndex;
    chain.push(Buffer.from(level[idx]));
    while (level.length > 1) {
      level = MerkleTree.parentLevel(level);
      idx = Math.floor(idx / 2);
      chain.push(Buffer.from(level[idx]));
    }
    return chain;
  }

  /**
   * 存证随根演进：擦除发生后，旧存证持有人无需访问树，
   * 仅凭公开的擦除回执即可把旧路径局部重放为新根下的有效路径。
   */
  static updateInclusionReceipt(receipt: InclusionReceipt, erase: EraseReceipt): InclusionReceipt {
    if (receipt.treeId !== erase.treeId) throw new Error('存证与擦除回执不属于同一棵树');
    if (receipt.treeSize !== erase.treeSize) throw new Error('树规模不一致，无法演进存证');
    if (receipt.leafIndex === erase.leafIndex) throw new Error('被擦除记录自身的存证不可演进');

    const levelHashes = erase.levelHashes;
    const newPath: InclusionReceipt['path'] = [];
    let size = erase.treeSize;
    let idx = receipt.leafIndex;
    let erasedIdx = erase.leafIndex;
    let stepIdx = 0;
    let level = 0;
    while (size > 1) {
      const sibling = idx % 2 === 0 ? idx + 1 : idx - 1;
      if (sibling < size) {
        const step = receipt.path[stepIdx++];
        // 本层兄弟恰为被擦除子树时，用擦除回执中的新哈希替换
        if (sibling === erasedIdx) {
          newPath.push({ position: step.position, hash: levelHashes[level] });
        } else {
          newPath.push(step);
        }
      }
      idx = Math.floor(idx / 2);
      erasedIdx = Math.floor(erasedIdx / 2);
      size = Math.ceil(size / 2);
      level++;
    }
    return { ...receipt, path: newPath };
  }

  private rebuild(): void {
    this.tree = new MerkleTree(this.slots.map((s) => (s.erased ? s.eraseMarkerHash! : s.leafHash)));
  }

  private assertSlot(index: number): number {
    if (!Number.isInteger(index) || index < 0 || index >= this.slots.length) {
      throw new RangeError(`记录下标越界: ${index}`);
    }
    return index;
  }
}

function serializeProof(proof: MerkleProof): EraseReceipt['merkleProof'] {
  return {
    leafIndex: proof.leafIndex,
    leafHash: proof.leafHash.toString('hex'),
    path: proof.path.map((s) => ({ position: s.position, hash: s.hash.toString('hex') })),
  };
}

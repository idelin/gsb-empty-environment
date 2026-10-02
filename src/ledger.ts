import { generateKeyPairSync, sign, verify, createPublicKey } from 'node:crypto';
import type { KeyObject } from 'node:crypto';
import {
  buildMerkleProof,
  computeMerkleRoot,
  sha256Hex,
  verifyMerkleProof,
} from './merkle.ts';
import type { MerkleProof } from './merkle.ts';

export const GENESIS_PREV_HASH = '0'.repeat(64);

export interface TransferPayload {
  kind: 'transfer';
  txId: string;
  from: string;
  to: string;
  amount: number;
  memo?: string;
}

export interface ReversalPayload {
  kind: 'reversal';
  txId: string;
  reversesTxId: string;
  from: string;
  to: string;
  amount: number;
  reason?: string;
}

export type Payload = TransferPayload | ReversalPayload;

export interface Block {
  readonly index: number;
  readonly tick: number;
  readonly prevHash: string;
  readonly payload: Payload;
  readonly receipt: string;
  readonly hash: string;
}

export interface LedgerOptions {
  initialBalances?: Record<string, number>;
  privateKey?: KeyObject;
}

export interface BlockProof {
  block: Block;
  merkle: MerkleProof;
}

function stableStringify(value: unknown): string {
  if (value === null || typeof value !== 'object') {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return '[' + value.map(stableStringify).join(',') + ']';
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  const parts = keys
    .filter((key) => record[key] !== undefined)
    .map((key) => JSON.stringify(key) + ':' + stableStringify(record[key]));
  return '{' + parts.join(',') + '}';
}

export function hashBlockContent(content: {
  index: number;
  tick: number;
  prevHash: string;
  payload: Payload;
}): string {
  return sha256Hex(stableStringify(content));
}

function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === 'object') {
    for (const key of Object.keys(value as Record<string, unknown>)) {
      deepFreeze((value as Record<string, unknown>)[key]);
    }
    Object.freeze(value);
  }
  return value;
}

export class Ledger {
  readonly #blocks: Block[] = [];
  readonly #balances = new Map<string, number>();
  readonly #txIndex = new Map<string, number>();
  readonly #reversedTxIds = new Set<string>();
  readonly #privateKey: KeyObject;
  readonly #publicKey: KeyObject;
  #tick = 0;

  constructor(options: LedgerOptions = {}) {
    if (options.privateKey) {
      this.#privateKey = options.privateKey;
      this.#publicKey = createPublicKey(options.privateKey);
    } else {
      const pair = generateKeyPairSync('ed25519');
      this.#privateKey = pair.privateKey;
      this.#publicKey = pair.publicKey;
    }
    for (const [account, amount] of Object.entries(options.initialBalances ?? {})) {
      if (!Number.isSafeInteger(amount) || amount < 0) {
        throw new Error(`invalid initial balance for ${account}`);
      }
      this.#balances.set(account, amount);
    }
  }

  get height(): number {
    return this.#blocks.length;
  }

  get tipHash(): string {
    return this.height === 0 ? GENESIS_PREV_HASH : this.#blocks[this.height - 1].hash;
  }

  get publicKeyPem(): string {
    return this.#publicKey.export({ type: 'spki', format: 'pem' }).toString();
  }

  getBlock(index: number): Block {
    const block = this.#blocks[index];
    if (!block) {
      throw new RangeError(`no block at index ${index}`);
    }
    return block;
  }

  getBlockByTxId(txId: string): Block {
    const index = this.#txIndex.get(txId);
    if (index === undefined) {
      throw new Error(`unknown transaction ${txId}`);
    }
    return this.#blocks[index];
  }

  balanceOf(account: string): number {
    return this.#balances.get(account) ?? 0;
  }

  balancesSnapshot(): Record<string, number> {
    return Object.fromEntries([...this.#balances.entries()].sort());
  }

  submitTransfer(input: {
    from: string;
    to: string;
    amount: number;
    memo?: string;
  }): Block {
    const { from, to, amount, memo } = input;
    this.#assertAccountName(from, 'from');
    this.#assertAccountName(to, 'to');
    if (from === to) {
      throw new Error('from and to must differ');
    }
    this.#assertAmount(amount);
    if (this.balanceOf(from) < amount) {
      throw new Error(`insufficient funds: ${from} has ${this.balanceOf(from)}, needs ${amount}`);
    }
    const payload: TransferPayload = {
      kind: 'transfer',
      txId: this.#nextTxId(),
      from,
      to,
      amount,
      ...(memo !== undefined ? { memo } : {}),
    };
    return this.#append(payload, (balances) => {
      balances.set(from, this.balanceOf(from) - amount);
      balances.set(to, this.balanceOf(to) + amount);
    });
  }

  reverse(txId: string, reason?: string): Block {
    const original = this.getBlockByTxId(txId);
    if (original.payload.kind !== 'transfer') {
      throw new Error(`transaction ${txId} is not a transfer and cannot be reversed`);
    }
    if (this.#reversedTxIds.has(txId)) {
      throw new Error(`transaction ${txId} has already been reversed`);
    }
    const { from, to, amount } = original.payload;
    if (this.balanceOf(to) < amount) {
      throw new Error(
        `cannot reverse ${txId}: ${to} no longer holds ${amount} (balance ${this.balanceOf(to)})`,
      );
    }
    const payload: ReversalPayload = {
      kind: 'reversal',
      txId: this.#nextTxId(),
      reversesTxId: txId,
      from: to,
      to: from,
      amount,
      ...(reason !== undefined ? { reason } : {}),
    };
    return this.#append(payload, (balances) => {
      balances.set(to, this.balanceOf(to) - amount);
      balances.set(from, this.balanceOf(from) + amount);
      this.#reversedTxIds.add(txId);
    });
  }

  isReversed(txId: string): boolean {
    return this.#reversedTxIds.has(txId);
  }

  merkleRoot(atHeight: number = this.height): string {
    return computeMerkleRoot(this.#leafHashes(atHeight));
  }

  getProof(index: number, atHeight: number = this.height): BlockProof {
    if (index < 0 || index >= this.height) {
      throw new RangeError(`no block at index ${index}`);
    }
    if (atHeight < index + 1 || atHeight > this.height) {
      throw new RangeError(
        `height ${atHeight} cannot prove block ${index} (current height ${this.height})`,
      );
    }
    const block = this.#blocks[index];
    return { block, merkle: buildMerkleProof(this.#leafHashes(atHeight), index) };
  }

  verifyChain(): boolean {
    let prevHash = GENESIS_PREV_HASH;
    for (let index = 0; index < this.#blocks.length; index++) {
      const block = this.#blocks[index];
      if (block.index !== index || block.prevHash !== prevHash) {
        return false;
      }
      const recomputed = hashBlockContent({
        index: block.index,
        tick: block.tick,
        prevHash: block.prevHash,
        payload: block.payload,
      });
      if (recomputed !== block.hash) {
        return false;
      }
      if (!Ledger.verifyReceipt(block, this.#publicKey)) {
        return false;
      }
      prevHash = block.hash;
    }
    return true;
  }

  static verifyProof(proof: BlockProof, publicKeyPem: string): boolean {
    const { block, merkle } = proof;
    const recomputed = hashBlockContent({
      index: block.index,
      tick: block.tick,
      prevHash: block.prevHash,
      payload: block.payload,
    });
    if (recomputed !== block.hash || merkle.leaf !== block.hash) {
      return false;
    }
    if (!verifyMerkleProof(merkle)) {
      return false;
    }
    return Ledger.verifyReceipt(block, createPublicKey(publicKeyPem));
  }

  static verifyReceipt(block: Block, publicKey: KeyObject): boolean {
    return verify(null, Buffer.from(block.hash, 'utf8'), publicKey, Buffer.from(block.receipt, 'hex'));
  }

  #nextTxId(): string {
    return `TX-${String(this.#blocks.length + 1).padStart(4, '0')}`;
  }

  #append(payload: Payload, apply: (balances: Map<string, number>) => void): Block {
    const index = this.#blocks.length;
    const tick = ++this.#tick;
    const prevHash = this.tipHash;
    const hash = hashBlockContent({ index, tick, prevHash, payload });
    const receipt = sign(null, Buffer.from(hash, 'utf8'), this.#privateKey).toString('hex');
    const block: Block = deepFreeze({ index, tick, prevHash, payload, receipt, hash });
    this.#blocks.push(block);
    apply(this.#balances);
    this.#txIndex.set(payload.txId, index);
    return block;
  }

  #leafHashes(atHeight: number): string[] {
    if (atHeight < 0 || atHeight > this.height) {
      throw new RangeError(`height ${atHeight} out of range (current height ${this.height})`);
    }
    return this.#blocks.slice(0, atHeight).map((block) => block.hash);
  }

  #assertAccountName(account: string, field: string): void {
    if (typeof account !== 'string' || account.length === 0) {
      throw new Error(`${field} must be a non-empty account name`);
    }
  }

  #assertAmount(amount: number): void {
    if (!Number.isSafeInteger(amount) || amount <= 0) {
      throw new Error(`amount must be a positive safe integer, got ${amount}`);
    }
  }
}

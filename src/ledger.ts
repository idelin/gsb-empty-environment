import {
  canonicalJson,
  generateLedgerKeyPair,
  sha256Hex,
  signHex,
  verifySignature,
  type KeyPair,
} from './crypto.ts';
import {
  generateProof,
  merkleRoot,
  verifyProof,
  type MerkleProof,
} from './merkle.ts';

export const GENESIS_HASH = '0'.repeat(64);

export interface TransferPayload {
  from: string;
  to: string;
  amount: number;
  asset: string;
  memo?: string;
}

export type BlockKind = 'TRANSFER' | 'REVERSAL';

export interface SignatureReceipt {
  signer: string;
  publicKey: string;
  signature: string;
}

export interface Block {
  index: number;
  logicalTime: number;
  kind: BlockKind;
  prevHash: string;
  payload: TransferPayload;
  reversalOf: number | null;
  receipt: SignatureReceipt;
  hash: string;
}

function signingMessage(block: Omit<Block, 'receipt' | 'hash'>): string {
  return canonicalJson(block);
}

function blockHash(block: Omit<Block, 'hash'>): string {
  return sha256Hex(canonicalJson(block));
}

export class Ledger {
  readonly signer: string;
  readonly #keyPair: KeyPair;
  readonly #blocks: Block[] = [];
  readonly #rootHistory: string[] = [];
  readonly #reversed = new Set<number>();
  #clock = 0;

  constructor(signer = 'ledger-authority', keyPair?: KeyPair) {
    this.signer = signer;
    this.#keyPair = keyPair ?? generateLedgerKeyPair();
  }

  get publicKey(): string {
    return this.#keyPair.publicKey;
  }

  get height(): number {
    return this.#blocks.length;
  }

  get logicalTime(): number {
    return this.#clock;
  }

  getBlock(index: number): Block {
    const block = this.#blocks[index - 1];
    if (!block) {
      throw new RangeError(`block #${index} does not exist`);
    }
    return Object.freeze({ ...block, payload: { ...block.payload } });
  }

  getRoot(height?: number): string {
    const h = height ?? this.#blocks.length;
    if (h < 1 || h > this.#rootHistory.length) {
      throw new RangeError(`no Merkle root recorded at height ${h}`);
    }
    return this.#rootHistory[h - 1];
  }

  appendTransfer(payload: TransferPayload): Block {
    this.#validateTransfer(payload);
    return this.#append('TRANSFER', payload, null);
  }

  reverse(originalIndex: number, memo?: string): Block {
    const original = this.#blocks[originalIndex - 1];
    if (!original) {
      throw new RangeError(`cannot reverse: block #${originalIndex} does not exist`);
    }
    if (original.kind !== 'TRANSFER') {
      throw new Error(`cannot reverse: block #${originalIndex} is not a transfer`);
    }
    if (this.#reversed.has(originalIndex)) {
      throw new Error(
        `cannot reverse: block #${originalIndex} has already been reversed`,
      );
    }
    const compensating: TransferPayload = {
      from: original.payload.to,
      to: original.payload.from,
      amount: original.payload.amount,
      asset: original.payload.asset,
      memo: memo ?? `reversal of block #${originalIndex}`,
    };
    const block = this.#append('REVERSAL', compensating, originalIndex);
    this.#reversed.add(originalIndex);
    return block;
  }

  isReversed(index: number): boolean {
    return this.#reversed.has(index);
  }

  getBalance(account: string, asset = 'COIN'): number {
    let balance = 0;
    for (const block of this.#blocks) {
      if (block.payload.asset !== asset) continue;
      if (block.payload.to === account) balance += block.payload.amount;
      if (block.payload.from === account) balance -= block.payload.amount;
    }
    return balance;
  }

  getProof(index: number): MerkleProof {
    const leaves = this.#blocks.map((b) => b.hash);
    return generateProof(leaves, index - 1);
  }

  verifyProofAtHeight(proof: MerkleProof, height?: number): boolean {
    return verifyProof(proof, this.getRoot(height));
  }

  verifyChain(): boolean {
    let prevHash = GENESIS_HASH;
    for (const block of this.#blocks) {
      if (block.prevHash !== prevHash) return false;
      const { hash, ...rest } = block;
      if (blockHash(rest) !== hash) return false;
      if (
        !verifySignature(
          block.receipt.publicKey,
          signingMessage({
            index: block.index,
            logicalTime: block.logicalTime,
            kind: block.kind,
            prevHash: block.prevHash,
            payload: block.payload,
            reversalOf: block.reversalOf,
          }),
          block.receipt.signature,
        )
      ) {
        return false;
      }
      prevHash = hash;
    }
    return true;
  }

  toJSON(): Block[] {
    return this.#blocks.map((b) => ({ ...b, payload: { ...b.payload } }));
  }

  #validateTransfer(payload: TransferPayload): void {
    if (!payload.from || !payload.to) {
      throw new Error('transfer requires from/to accounts');
    }
    if (payload.from === payload.to) {
      throw new Error('transfer from and to must differ');
    }
    if (!Number.isFinite(payload.amount) || payload.amount <= 0) {
      throw new Error('transfer amount must be a positive finite number');
    }
    if (!payload.asset) {
      throw new Error('transfer requires an asset');
    }
  }

  #append(
    kind: BlockKind,
    payload: TransferPayload,
    reversalOf: number | null,
  ): Block {
    this.#clock += 1;
    const unsigned = {
      index: this.#blocks.length + 1,
      logicalTime: this.#clock,
      kind,
      prevHash:
        this.#blocks.length === 0
          ? GENESIS_HASH
          : this.#blocks[this.#blocks.length - 1].hash,
      payload,
      reversalOf,
    };
    const receipt: SignatureReceipt = {
      signer: this.signer,
      publicKey: this.#keyPair.publicKey,
      signature: signHex(this.#keyPair.privateKey, signingMessage(unsigned)),
    };
    const block: Block = {
      ...unsigned,
      receipt,
      hash: blockHash({ ...unsigned, receipt }),
    };
    this.#blocks.push(block);
    this.#rootHistory.push(merkleRoot(this.#blocks.map((b) => b.hash)));
    return Object.freeze({ ...block, payload: { ...block.payload } });
  }
}

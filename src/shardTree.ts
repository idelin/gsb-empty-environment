import { MerkleTree } from './merkleTree.ts';
import type { MerkleProof } from './merkleTree.ts';

export interface AccountState {
  balance: bigint;
  nonce: number;
}

export interface ShardInclusionProof {
  shardId: number;
  accountId: string;
  state: AccountState;
  leafHash: string; // hex
  proof: MerkleProof;
  shardRoot: string; // hex
}

/**
 * Deterministic canonical encoding of an account record into leaf bytes.
 * Layout: "acct:v1" | shardId | accountId | balance | nonce
 */
export function encodeAccountLeaf(shardId: number, accountId: string, state: AccountState): Buffer {
  return Buffer.from(
    `acct:v1|shard=${shardId}|id=${accountId}|balance=${state.balance.toString()}|nonce=${state.nonce}`,
    'utf8',
  );
}

/**
 * One shard's account-state Merkle tree. Accounts are dynamically added or
 * updated; the shard maintains its own local root independently.
 */
export class ShardTree {
  readonly shardId: number;
  private tree = new MerkleTree();
  private accounts = new Map<string, { index: number; state: AccountState }>();

  constructor(shardId: number) {
    this.shardId = shardId;
  }

  get size(): number {
    return this.accounts.size;
  }

  hasAccount(accountId: string): boolean {
    return this.accounts.has(accountId);
  }

  getAccount(accountId: string): AccountState {
    const entry = this.accounts.get(accountId);
    if (!entry) throw new Error(`unknown account "${accountId}" in shard ${this.shardId}`);
    return { balance: entry.state.balance, nonce: entry.state.nonce };
  }

  /** Insert a new account or update an existing one, refreshing the local root. */
  setAccount(accountId: string, state: AccountState): void {
    const leaf = encodeAccountLeaf(this.shardId, accountId, state);
    const existing = this.accounts.get(accountId);
    if (existing) {
      this.tree.update(existing.index, leaf);
      existing.state = { balance: state.balance, nonce: state.nonce };
    } else {
      const index = this.tree.append(leaf);
      this.accounts.set(accountId, { index, state: { balance: state.balance, nonce: state.nonce } });
    }
  }

  /** Intra-shard transfer: debit sender, credit receiver, bump sender nonce. */
  applyTransfer(fromId: string, toId: string, amount: bigint): void {
    if (amount <= 0n) throw new Error('transfer amount must be positive');
    const from = this.accounts.get(fromId);
    const to = this.accounts.get(toId);
    if (!from) throw new Error(`unknown sender "${fromId}" in shard ${this.shardId}`);
    if (!to) throw new Error(`unknown receiver "${toId}" in shard ${this.shardId}`);
    if (from.state.balance < amount) throw new Error('insufficient balance');
    this.setAccount(fromId, {
      balance: from.state.balance - amount,
      nonce: from.state.nonce + 1,
    });
    this.setAccount(toId, {
      balance: to.state.balance + amount,
      nonce: to.state.nonce,
    });
  }

  root(): Buffer {
    return this.tree.root();
  }

  /** Inclusion proof for an account against this shard's current local root. */
  getInclusionProof(accountId: string): ShardInclusionProof {
    const entry = this.accounts.get(accountId);
    if (!entry) throw new Error(`unknown account "${accountId}" in shard ${this.shardId}`);
    const proof = this.tree.getProof(entry.index);
    return {
      shardId: this.shardId,
      accountId,
      state: { balance: entry.state.balance, nonce: entry.state.nonce },
      leafHash: proof.leafHash,
      proof,
      shardRoot: this.tree.root().toString('hex'),
    };
  }
}

import { MerkleTree } from './merkle.ts';
import type { ProofStep } from './merkle.ts';

export interface AccountState {
  id: string;
  balance: number;
}

/** Inclusion proof of one account inside its shard tree. */
export interface AccountInclusionProof {
  shardId: number;
  account: AccountState;
  leafIndex: number;
  proof: ProofStep[];
  shardRoot: string;
}

/** Deterministic leaf encoding for an account state. */
export function encodeAccount(account: AccountState): Buffer {
  if (!Number.isSafeInteger(account.balance) || account.balance < 0) {
    throw new RangeError(`invalid balance for account ${account.id}: ${account.balance}`);
  }
  return Buffer.from(`account:${account.id}:${account.balance}`, 'utf8');
}

/**
 * One shard of the system: owns a set of accounts and maintains its own
 * local Merkle root, updated independently of all other shards.
 */
export class ShardTree {
  readonly shardId: number;
  private readonly tree = new MerkleTree(4);
  private readonly accounts = new Map<string, AccountState>();
  private readonly indexByAccount = new Map<string, number>();
  private nextIndex = 0;

  constructor(shardId: number, initialAccounts: AccountState[] = []) {
    this.shardId = shardId;
    for (const account of initialAccounts) this.upsertAccount(account);
  }

  get accountCount(): number {
    return this.accounts.size;
  }

  getAccount(id: string): AccountState | undefined {
    const account = this.accounts.get(id);
    return account ? { ...account } : undefined;
  }

  upsertAccount(account: AccountState): void {
    let index = this.indexByAccount.get(account.id);
    if (index === undefined) {
      index = this.nextIndex++;
      this.indexByAccount.set(account.id, index);
    }
    this.accounts.set(account.id, { ...account });
    this.tree.setLeaf(index, encodeAccount(account));
  }

  /** Applies a balance transfer between two accounts inside this shard. */
  applyTransfer(fromId: string, toId: string, amount: number): void {
    const from = this.accounts.get(fromId);
    const to = this.accounts.get(toId);
    if (!from) throw new Error(`unknown sender account: ${fromId}`);
    if (!to) throw new Error(`unknown recipient account: ${toId}`);
    if (!Number.isSafeInteger(amount) || amount <= 0) {
      throw new RangeError(`transfer amount must be a positive integer, got ${amount}`);
    }
    if (from.balance < amount) {
      throw new Error(`insufficient funds: ${fromId} has ${from.balance}, needs ${amount}`);
    }
    this.upsertAccount({ id: fromId, balance: from.balance - amount });
    this.upsertAccount({ id: toId, balance: to.balance + amount });
  }

  /** Current local root of this shard, hex encoded. */
  getLocalRoot(): string {
    return this.tree.getRoot().toString('hex');
  }

  /** Builds the shard-internal inclusion proof for one account. */
  getAccountProof(accountId: string): AccountInclusionProof {
    const account = this.accounts.get(accountId);
    if (!account) throw new Error(`unknown account: ${accountId}`);
    const leafIndex = this.indexByAccount.get(accountId)!;
    return {
      shardId: this.shardId,
      account: { ...account },
      leafIndex,
      proof: this.tree.getProof(leafIndex),
      shardRoot: this.getLocalRoot(),
    };
  }
}

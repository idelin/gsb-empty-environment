export type UserId = string;
export type RoleId = string;
export type Permission = string;

/** Half-open interval [from, to). `Infinity` means open-ended. */
export interface Interval {
  readonly from: number;
  readonly to: number;
}

export interface Role {
  readonly id: RoleId;
  readonly permissions: ReadonlySet<Permission>;
}

interface Bitemporal {
  /** Business (valid) time: when the fact is effective in the real world. */
  readonly valid: Interval;
  /** Audit (transaction/knowledge) time: when the system knew the fact. */
  known: Interval;
}

export interface GrantVersion extends Bitemporal {
  readonly kind: "grant";
  readonly user: UserId;
  readonly role: RoleId;
}

export interface DelegationVersion extends Bitemporal {
  readonly kind: "delegation";
  readonly delegator: UserId;
  readonly delegatee: UserId;
  readonly role: RoleId;
}

export type RecordVersion = GrantVersion | DelegationVersion;

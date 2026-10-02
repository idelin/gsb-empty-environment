/** 租约仍被其他机房持有（法官拒绝颁发，防止双主）。 */
export class LeaseHeldError extends Error {
  readonly holderId: string;
  readonly epoch: number;
  readonly expiresAt: number;
  constructor(holderId: string, epoch: number, expiresAt: number) {
    super(`lease is actively held by "${holderId}" (epoch=${epoch}, expiresAt=${expiresAt})`);
    this.name = 'LeaseHeldError';
    this.holderId = holderId;
    this.epoch = epoch;
    this.expiresAt = expiresAt;
  }
}

/** 租约凭证已被法官作废（epoch 不再是最新），持证人必须降级。 */
export class StaleLeaseCredentialError extends Error {
  readonly dcId: string;
  readonly epoch: number;
  constructor(dcId: string, epoch: number) {
    super(`lease credential of "${dcId}" (epoch=${epoch}) is no longer valid`);
    this.name = 'StaleLeaseCredentialError';
    this.dcId = dcId;
    this.epoch = epoch;
  }
}

/** 网络不可达（光纤挖断 / 机房失联）。 */
export class NetworkUnreachableError extends Error {
  constructor(from: string, to: string) {
    super(`network unreachable: ${from} -> ${to}`);
    this.name = 'NetworkUnreachableError';
  }
}

/** 当前节点不是可写主节点。 */
export class NotWritableError extends Error {
  constructor(dcId: string, reason: string) {
    super(`datacenter "${dcId}" is not writable: ${reason}`);
    this.name = 'NotWritableError';
  }
}

/** 存储层 Fencing：携带过期 epoch 的僵尸写入被拒绝。 */
export class StaleFenceError extends Error {
  readonly epoch: number;
  readonly fencedEpoch: number;
  constructor(epoch: number, fencedEpoch: number) {
    super(`write fenced: epoch=${epoch} < highest-seen epoch=${fencedEpoch}`);
    this.name = 'StaleFenceError';
    this.epoch = epoch;
    this.fencedEpoch = fencedEpoch;
  }
}

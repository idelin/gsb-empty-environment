import type { PartitionId } from './store.ts';

/** 换节点策略下所有候选副本都落后于会话水位。 */
export class NoFreshReplicaError extends Error {
  readonly partition: PartitionId;
  readonly requiredVersion: number;

  constructor(partition: PartitionId, requiredVersion: number) {
    super(`partition "${partition}": no replica has reached version ${requiredVersion}`);
    this.name = 'NoFreshReplicaError';
    this.partition = partition;
    this.requiredVersion = requiredVersion;
  }
}

/** 超时策略下等待副本追平超过时限。 */
export class ReadTimeoutError extends Error {
  readonly partition: PartitionId;
  readonly requiredVersion: number;
  readonly timeoutMs: number;

  constructor(partition: PartitionId, requiredVersion: number, timeoutMs: number) {
    super(
      `partition "${partition}": timed out after ${timeoutMs}ms waiting for version ${requiredVersion}`,
    );
    this.name = 'ReadTimeoutError';
    this.partition = partition;
    this.requiredVersion = requiredVersion;
    this.timeoutMs = timeoutMs;
  }
}

/** 防御性不变量：路由层绝不返回比会话水位更旧的版本。触发即表示内部 bug。 */
export class StaleReadError extends Error {
  readonly partition: PartitionId;
  readonly requiredVersion: number;
  readonly returnedVersion: number;

  constructor(partition: PartitionId, requiredVersion: number, returnedVersion: number) {
    super(
      `partition "${partition}": returned version ${returnedVersion} is older than session watermark ${requiredVersion}`,
    );
    this.name = 'StaleReadError';
    this.partition = partition;
    this.requiredVersion = requiredVersion;
    this.returnedVersion = returnedVersion;
  }
}

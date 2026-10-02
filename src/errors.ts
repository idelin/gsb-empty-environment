/** 租约已丢失（过期或被服务端回收），持有者不再拥有锁。 */
export class LeaseLostError extends Error {
  constructor(message = "lease is no longer held (expired or revoked)") {
    super(message);
    this.name = "LeaseLostError";
  }
}

/** 尝试释放/续期一个不属于自己（或不存在）的租约。 */
export class NotLockHolderError extends Error {
  constructor(message = "caller does not hold this lease") {
    super(message);
    this.name = "NotLockHolderError";
  }
}

/** 携带过期 fencing token 的写操作被受保护资源拒绝。 */
export class StaleFenceTokenError extends Error {
  readonly providedToken: number;
  readonly currentToken: number;

  constructor(providedToken: number, currentToken: number) {
    super(
      `stale fence token: provided=${providedToken}, ` +
        `but the resource has already seen token=${currentToken}`,
    );
    this.name = "StaleFenceTokenError";
    this.providedToken = providedToken;
    this.currentToken = currentToken;
  }
}

/** 排队等待获取锁时被 AbortSignal 取消。 */
export class AcquireAbortedError extends Error {
  constructor(message = "acquire was aborted while waiting in queue") {
    super(message);
    this.name = "AcquireAbortedError";
  }
}

/**
 * 版本追踪器：为缓存输入提供「真实底层身份 + 单调版本」。
 *
 * 关键约定：TypedArray / DataView 等视图共享同一个底层 ArrayBuffer，
 * 因此身份与版本都挂在底层 buffer 上，而不是挂在视图对象上。
 * 通过任何一个视图（subarray 等）修改数据后调用 touch/mutate，
 * 所有共享该 buffer 的视图都会看到版本推进。
 *
 * 非视图对象（普通数组、普通对象）的身份与版本挂在对象本身上。
 * 调用方原地修改输入后必须通过 touch/mutate 上报，否则缓存无从得知。
 */
export class VersionTracker {
  private readonly versions = new WeakMap<object, number>();
  private readonly identities = new WeakMap<object, number>();
  private nextIdentity = 0;

  /** 输入的「真实底层对象」：视图归一到其 ArrayBuffer，其余归一到自身。 */
  static underlyingOf(input: unknown): object {
    if (input === null || typeof input !== 'object') {
      throw new TypeError(
        'cache input must be an object (typed array, view, plain array, or plain object)',
      );
    }
    if (ArrayBuffer.isView(input)) {
      return input.buffer as object;
    }
    return input;
  }

  /**
   * 视图形状描述：同一底层 buffer 上不同范围/类型的视图是不同的逻辑输入，
   * 必须参与缓存键，否则 subarray 之间会互相误命中。
   */
  static shapeOf(input: unknown): string {
    if (ArrayBuffer.isView(input)) {
      const view = input as ArrayBufferView;
      return `${view.constructor.name}@${view.byteOffset}+${view.byteLength}`;
    }
    return '-';
  }

  /** 底层对象的稳定身份编号（仅在本 tracker 内有意义，用于可读键）。 */
  identityOf(input: unknown): number {
    const target = VersionTracker.underlyingOf(input);
    let id = this.identities.get(target);
    if (id === undefined) {
      this.nextIdentity += 1;
      id = this.nextIdentity;
      this.identities.set(target, id);
    }
    return id;
  }

  /** 底层对象当前版本（从未上报过修改为 0）。 */
  versionOf(input: unknown): number {
    return this.versions.get(VersionTracker.underlyingOf(input)) ?? 0;
  }

  /** 上报一次原地修改，返回推进后的新版本号。 */
  touch(input: unknown): number {
    const target = VersionTracker.underlyingOf(input);
    const next = (this.versions.get(target) ?? 0) + 1;
    this.versions.set(target, next);
    return next;
  }

  /** 执行原地修改并上报版本推进。 */
  mutate<T>(input: T, fn: (input: T) => void): number {
    fn(input);
    return this.touch(input);
  }
}

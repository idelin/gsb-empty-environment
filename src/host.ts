import { TransientActivationTracker } from "./activation.ts";
import { StorageAccessError, reject } from "./errors.ts";
import { GrantStore } from "./grants.ts";
import { assertSandboxToken } from "./sandbox.ts";
import { grantKey, sameSite, siteOf } from "./site.ts";
import type {
  Clock,
  FrameContext,
  FrameId,
  SandboxTokenSet,
  Site,
  StorageAccessHostOptions,
} from "./types.ts";

const SYSTEM_CLOCK: Clock = { now: () => Date.now() };

/**
 * Storage Access 授权宿主。
 *
 * 安全约束：
 * 1. requestStorageAccess 只能由嵌入文档的瞬时用户激活触发，激活过期即拒绝；
 * 2. 授权按 (嵌入站点, 顶层站点) 二元组隔离存储；
 * 3. sandbox 文档缺少 allow-storage-access-by-user-activation 令牌时拒绝；
 * 4. 顶层跨站导航使旧顶层站点下的授权全部失效，并清空瞬时激活。
 */
export class StorageAccessHost {
  private readonly frames = new Map<FrameId, FrameContext>();
  private readonly activation: TransientActivationTracker;
  private readonly grantStore = new GrantStore();
  private readonly clock: Clock;
  private topLevelOrigin: string | null = null;

  constructor(options: StorageAccessHostOptions = {}) {
    this.clock = options.clock ?? SYSTEM_CLOCK;
    this.activation = new TransientActivationTracker(
      this.clock,
      options.activationTtlMs,
    );
    if (options.frames) {
      for (const frame of options.frames) {
        this.addFrame(frame);
      }
    }
  }

  addFrame(frame: FrameContext): void {
    if (this.frames.has(frame.id)) {
      throw new Error(`frame already registered: ${frame.id}`);
    }
    this.frames.set(frame.id, frame);
    if (frame.isTopLevel) {
      this.topLevelOrigin = frame.topLevelOrigin;
    }
  }

  getFrame(frameId: FrameId): FrameContext {
    const frame = this.frames.get(frameId);
    if (!frame) {
      throw new StorageAccessError(
        "InvalidStateError",
        `unknown frame: ${frameId}`,
      );
    }
    return frame;
  }

  updateSandboxTokens(frameId: FrameId, tokens: SandboxTokenSet | null): void {
    const frame = this.getFrame(frameId);
    this.frames.set(frameId, { ...frame, sandboxTokens: tokens });
  }

  /** 嵌入文档收到一次用户手势（点击等），刷新其瞬时激活。 */
  notifyUserGesture(frameId: FrameId): void {
    this.getFrame(frameId);
    this.activation.notifyUserGesture(frameId);
  }

  hasTransientActivation(frameId: FrameId): boolean {
    return this.activation.isActive(frameId);
  }

  /**
   * 对应 document.requestStorageAccess()。
   * 始终返回 Promise；所有拒绝都以规范风格的 DOMException 名字体现。
   */
  requestStorageAccess(frameId: FrameId): Promise<void> {
    let frame: FrameContext;
    try {
      frame = this.getFrame(frameId);
    } catch (error) {
      return reject(
        (error as StorageAccessError).name ?? "InvalidStateError",
        (error as Error).message,
      );
    }

    if (frame.isTopLevel) {
      return Promise.resolve();
    }

    if (sameSite(frame.origin, frame.topLevelOrigin)) {
      return Promise.resolve();
    }

    try {
      assertSandboxToken(frame.sandboxTokens);
    } catch (error) {
      const domError = error as StorageAccessError;
      return reject(domError.name, domError.message);
    }

    if (!this.activation.consume(frameId)) {
      return reject(
        "NotAllowedError",
        "requestStorageAccess requires transient user activation " +
          "on the embedded document",
      );
    }

    const key = grantKey(frame.origin, frame.topLevelOrigin);
    this.grantStore.grant(key, frameId, this.clock.now());
    return Promise.resolve();
  }

  /** 对应 document.hasStorageAccess()。 */
  hasStorageAccess(frameId: FrameId): boolean {
    const frame = this.frames.get(frameId);
    if (!frame) {
      return false;
    }
    if (frame.isTopLevel) {
      return true;
    }
    if (sameSite(frame.origin, frame.topLevelOrigin)) {
      return true;
    }
    return this.grantStore.has(
      grantKey(frame.origin, frame.topLevelOrigin),
    );
  }

  /**
   * 顶层文档导航。
   * 跨站：撤销旧顶层站点下的全部授权；任何导航都清空瞬时激活。
   */
  navigateTopLevel(newTopLevelOrigin: string): {
    crossSite: boolean;
    revokedGrants: number;
  } {
    if (!this.topLevelOrigin) {
      throw new StorageAccessError(
        "InvalidStateError",
        "no top-level frame registered",
      );
    }

    const oldSite: Site = siteOf(this.topLevelOrigin);
    const newSite: Site = siteOf(newTopLevelOrigin);
    const crossSite = oldSite !== newSite;

    let revokedGrants = 0;
    if (crossSite) {
      revokedGrants = this.grantStore.revokeForTopLevel(oldSite);
    }

    this.activation.clearAll();
    this.topLevelOrigin = newTopLevelOrigin;
    for (const [id, frame] of this.frames) {
      this.frames.set(id, { ...frame, topLevelOrigin: newTopLevelOrigin });
    }

    return { crossSite, revokedGrants };
  }

  removeFrame(frameId: FrameId): void {
    if (this.frames.delete(frameId)) {
      this.activation.clear(frameId);
      this.grantStore.revokeForFrame(frameId);
    }
  }
}

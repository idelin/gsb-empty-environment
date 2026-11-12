import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  ALLOW_STORAGE_ACCESS_BY_USER_ACTIVATION,
  StorageAccessError,
  StorageAccessHost,
  type Clock,
  type FrameContext,
} from "../index.ts";

class FakeClock implements Clock {
  current = 0;
  now(): number {
    return this.current;
  }
  advance(ms: number): void {
    this.current += ms;
  }
}

const TTL_MS = 5000;

function makeHost(clock: FakeClock): StorageAccessHost {
  return new StorageAccessHost({
    clock,
    activationTtlMs: TTL_MS,
    frames: [
      {
        id: "top",
        origin: "https://publisher.example",
        topLevelOrigin: "https://publisher.example",
        isTopLevel: true,
        sandboxTokens: null,
      },
      {
        id: "iframe",
        origin: "https://ads.attacker.test",
        topLevelOrigin: "https://publisher.example",
        isTopLevel: false,
        sandboxTokens: null,
      },
    ],
  });
}

function sandboxedFrame(tokens: ReadonlySet<string> | null): FrameContext {
  return {
    id: "boxed",
    origin: "https://ads.attacker.test",
    topLevelOrigin: "https://publisher.example",
    isTopLevel: false,
    sandboxTokens: tokens,
  };
}

async function assertRejected(
  promise: Promise<unknown>,
  errorName: string,
): Promise<void> {
  await assert.rejects(promise, (error: unknown) => {
    assert.ok(error instanceof StorageAccessError);
    assert.equal(error.name, errorName);
    return true;
  });
}

describe("requestStorageAccess 手势与激活", () => {
  it("激活窗口内的手势请求成功并落库授权", async () => {
    const clock = new FakeClock();
    const host = makeHost(clock);

    assert.equal(host.hasStorageAccess("iframe"), false);
    host.notifyUserGesture("iframe");
    clock.advance(TTL_MS - 1);

    await host.requestStorageAccess("iframe");
    assert.equal(host.hasStorageAccess("iframe"), true);
  });

  it("瞬时激活过期后请求被拒绝（激活过期测试）", async () => {
    const clock = new FakeClock();
    const host = makeHost(clock);

    host.notifyUserGesture("iframe");
    clock.advance(TTL_MS);

    assert.equal(host.hasTransientActivation("iframe"), false);
    await assertRejected(
      host.requestStorageAccess("iframe"),
      "NotAllowedError",
    );
    assert.equal(host.hasStorageAccess("iframe"), false);
  });

  it("从未发生手势时请求被拒绝", async () => {
    const clock = new FakeClock();
    const host = makeHost(clock);

    await assertRejected(
      host.requestStorageAccess("iframe"),
      "NotAllowedError",
    );
  });

  it("激活是一次性的：成功请求消费后再次请求被拒绝", async () => {
    const clock = new FakeClock();
    const host = makeHost(clock);

    host.notifyUserGesture("iframe");
    await host.requestStorageAccess("iframe");

    await assertRejected(
      host.requestStorageAccess("iframe"),
      "NotAllowedError",
    );
  });

  it("过期边界：TTL-1ms 有效，恰好 TTL 时失效", async () => {
    const nearExpiry = new FakeClock();
    const hostNear = makeHost(nearExpiry);
    hostNear.notifyUserGesture("iframe");
    nearExpiry.advance(TTL_MS - 1);
    assert.equal(hostNear.hasTransientActivation("iframe"), true);
    await hostNear.requestStorageAccess("iframe");

    const atExpiry = new FakeClock();
    const hostAt = makeHost(atExpiry);
    hostAt.notifyUserGesture("iframe");
    atExpiry.advance(TTL_MS);
    assert.equal(hostAt.hasTransientActivation("iframe"), false);
    await assertRejected(
      hostAt.requestStorageAccess("iframe"),
      "NotAllowedError",
    );
  });
});

describe("sandbox 令牌", () => {
  it("sandbox 且缺少 storage-access 令牌时 SecurityError 拒绝", async () => {
    const clock = new FakeClock();
    const host = new StorageAccessHost({
      clock,
      activationTtlMs: TTL_MS,
      frames: [
        {
          id: "top",
          origin: "https://publisher.example",
          topLevelOrigin: "https://publisher.example",
          isTopLevel: true,
          sandboxTokens: null,
        },
        sandboxedFrame(new Set(["allow-scripts"])),
      ],
    });
    host.notifyUserGesture("boxed");

    await assertRejected(
      host.requestStorageAccess("boxed"),
      "SecurityError",
    );
    assert.equal(host.hasStorageAccess("boxed"), false);
  });

  it("sandbox 空令牌集合同样拒绝", async () => {
    const clock = new FakeClock();
    const host = new StorageAccessHost({
      clock,
      activationTtlMs: TTL_MS,
      frames: [
        {
          id: "top",
          origin: "https://publisher.example",
          topLevelOrigin: "https://publisher.example",
          isTopLevel: true,
          sandboxTokens: null,
        },
        sandboxedFrame(new Set()),
      ],
    });
    host.notifyUserGesture("boxed");

    await assertRejected(
      host.requestStorageAccess("boxed"),
      "SecurityError",
    );
  });

  it("sandbox 携带正确令牌且有手势时允许", async () => {
    const clock = new FakeClock();
    const host = new StorageAccessHost({
      clock,
      activationTtlMs: TTL_MS,
      frames: [
        {
          id: "top",
          origin: "https://publisher.example",
          topLevelOrigin: "https://publisher.example",
          isTopLevel: true,
          sandboxTokens: null,
        },
        sandboxedFrame(
          new Set([
            "allow-scripts",
            ALLOW_STORAGE_ACCESS_BY_USER_ACTIVATION,
          ]),
        ),
      ],
    });
    host.notifyUserGesture("boxed");

    await host.requestStorageAccess("boxed");
    assert.equal(host.hasStorageAccess("boxed"), true);
  });
});

describe("授权键 (嵌入站点, 顶层站点) 与顶层换站", () => {
  it("授权按顶层站点隔离：同一嵌入方换顶层后查不到旧授权", async () => {
    const clock = new FakeClock();
    const host = makeHost(clock);

    host.notifyUserGesture("iframe");
    await host.requestStorageAccess("iframe");
    assert.equal(host.hasStorageAccess("iframe"), true);

    const result = host.navigateTopLevel("https://other-publisher.example");
    assert.equal(result.crossSite, true);
    assert.equal(result.revokedGrants, 1);
    assert.equal(host.hasStorageAccess("iframe"), false);
  });

  it("顶层换站同时清空瞬时激活，旧手势无法在新站点复用", async () => {
    const clock = new FakeClock();
    const host = makeHost(clock);

    host.notifyUserGesture("iframe");
    host.navigateTopLevel("https://other-publisher.example");

    assert.equal(host.hasTransientActivation("iframe"), false);
    await assertRejected(
      host.requestStorageAccess("iframe"),
      "NotAllowedError",
    );
  });

  it("换站后在新顶层站点重新手势授权成功，且不恢复旧站点授权", async () => {
    const clock = new FakeClock();
    const host = makeHost(clock);

    host.notifyUserGesture("iframe");
    await host.requestStorageAccess("iframe");
    host.navigateTopLevel("https://other-publisher.example");

    host.notifyUserGesture("iframe");
    await host.requestStorageAccess("iframe");
    assert.equal(host.hasStorageAccess("iframe"), true);

    host.navigateTopLevel("https://publisher.example");
    assert.equal(host.hasStorageAccess("iframe"), false);
  });

  it("同站导航（仅路径/子域变化）不撤销授权", () => {
    const clock = new FakeClock();
    const host = makeHost(clock);

    const result = host.navigateTopLevel("https://login.publisher.example");
    assert.equal(result.crossSite, false);
    assert.equal(result.revokedGrants, 0);
  });

  it("不同嵌入站点的授权互不影响", async () => {
    const clock = new FakeClock();
    const host = new StorageAccessHost({
      clock,
      activationTtlMs: TTL_MS,
      frames: [
        {
          id: "top",
          origin: "https://publisher.example",
          topLevelOrigin: "https://publisher.example",
          isTopLevel: true,
          sandboxTokens: null,
        },
        {
          id: "a",
          origin: "https://ads.attacker.test",
          topLevelOrigin: "https://publisher.example",
          isTopLevel: false,
          sandboxTokens: null,
        },
        {
          id: "b",
          origin: "https://widget.vendor.test",
          topLevelOrigin: "https://publisher.example",
          isTopLevel: false,
          sandboxTokens: null,
        },
      ],
    });

    host.notifyUserGesture("a");
    await host.requestStorageAccess("a");

    assert.equal(host.hasStorageAccess("a"), true);
    assert.equal(host.hasStorageAccess("b"), false);
  });
});

describe("同站与顶层文档", () => {
  it("同站嵌入文档默认有存储访问，无需手势", async () => {
    const clock = new FakeClock();
    const host = new StorageAccessHost({
      clock,
      activationTtlMs: TTL_MS,
      frames: [
        {
          id: "top",
          origin: "https://publisher.example",
          topLevelOrigin: "https://publisher.example",
          isTopLevel: true,
          sandboxTokens: null,
        },
        {
          id: "same",
          origin: "https://login.publisher.example",
          topLevelOrigin: "https://publisher.example",
          isTopLevel: false,
          sandboxTokens: new Set(),
        },
      ],
    });

    await host.requestStorageAccess("same");
    assert.equal(host.hasStorageAccess("same"), true);
  });

  it("顶层文档恒为有访问权", async () => {
    const clock = new FakeClock();
    const host = makeHost(clock);

    await host.requestStorageAccess("top");
    assert.equal(host.hasStorageAccess("top"), true);
  });
});

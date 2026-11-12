import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  StorageAccessManager,
  namespacedKey,
} from "../src/storage-access.ts";
import {
  EMBED_X,
  GateBackend,
  KEY,
  NAMESPACE,
  SITE_A,
  SITE_B,
  countingPrompt,
} from "./helpers.ts";

describe("并行授权", () => {
  it("相同上下文的并行授权请求只提示一次并共享结果", async () => {
    let resolvePrompt: (approved: boolean) => void = () => {};
    const promptGate = new Promise<boolean>((resolve) => {
      resolvePrompt = resolve;
    });
    const prompt = countingPrompt(() => promptGate);
    const manager = new StorageAccessManager({ prompt: prompt.handler });
    const accessor = manager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_X,
    });

    const requests = [
      accessor.requestAccess({ namespace: NAMESPACE }),
      accessor.requestAccess({ namespace: NAMESPACE }),
      accessor.requestAccess({ namespace: NAMESPACE }),
    ];

    // 提示尚未返回前，三个请求都处于 in-flight 状态。
    assert.equal(prompt.calls.length, 1);
    resolvePrompt(true);

    const grants = await Promise.all(requests);
    assert.equal(grants[0].grantId, grants[1].grantId);
    assert.equal(grants[1].grantId, grants[2].grantId);
    assert.equal(manager.listGrants().length, 1);
  });

  it("不同命名空间的并行授权各自独立签发", async () => {
    const prompt = countingPrompt(true);
    const manager = new StorageAccessManager({ prompt: prompt.handler });
    const accessor = manager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_X,
    });

    const [a, b] = await Promise.all([
      accessor.requestAccess({ namespace: "ns-a" }),
      accessor.requestAccess({ namespace: "ns-b" }),
    ]);

    assert.notEqual(a.grantId, b.grantId);
    assert.equal(prompt.calls.length, 2);
    assert.equal(a.namespace, "ns-a");
    assert.equal(b.namespace, "ns-b");
  });

  it("被拒绝的 in-flight 请求不会缓存为成功授权", async () => {
    const prompt = countingPrompt(false);
    const manager = new StorageAccessManager({ prompt: prompt.handler });
    const accessor = manager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_X,
    });

    await Promise.allSettled([
      accessor.requestAccess({ namespace: NAMESPACE }),
      accessor.requestAccess({ namespace: NAMESPACE }),
    ]);
    assert.equal(manager.listGrants().length, 0);

    // 去重记录已清理，后续允许时可重新签发。
    const retryManager = new StorageAccessManager({
      prompt: (() => {
        let deniedOnce = false;
        return () => {
          if (!deniedOnce) {
            deniedOnce = true;
            return false;
          }
          return true;
        };
      })(),
    });
    const retryAccessor = retryManager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_X,
    });
    await assert.rejects(() => retryAccessor.requestAccess({ namespace: NAMESPACE }));
    const grant = await retryAccessor.requestAccess({ namespace: NAMESPACE });
    assert.equal(grant.namespace, NAMESPACE);
  });
});

describe("导航与旧事务的确定归属", () => {
  it("入队后导航的写事务确定归属为旧文档代并被拒绝", async () => {
    const gate = new GateBackend();
    const manager = new StorageAccessManager({ unpartitioned: gate });
    const accessor = manager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_X,
    });
    const grant = await accessor.requestAccess({ namespace: NAMESPACE });

    gate.arm();
    const head = accessor.set("head-key", "head-value");
    await gate.waitUntilBlocked();

    // 目标事务在队列中等待，入队时冻结为文档代 0。
    const pendingWrite = accessor.set(KEY, "old-generation-write");

    // 头部任务阻塞期间导航：文档代从 0 前进到 1。
    const newGeneration = accessor.navigate();
    assert.equal(newGeneration, 1);
    gate.release();

    await head;
    const result = await pendingWrite;
    assert.equal(result.ok, false);
    assert.equal(result.status, "denied-stale");

    const record = manager.ledger.at(-1)!;
    assert.equal(record.grantId, grant.grantId);
    assert.equal(record.enqueuedGeneration, 0);
    assert.equal(record.executedGeneration, 1);
    assert.equal(record.status, "denied-stale");

    // 被拒绝的写没有触碰后端。
    assert.equal(
      await gate.get(namespacedKey(NAMESPACE, KEY)),
      null,
    );
  });

  it("导航后新文档代的事务与旧事务在审计日志中各自可辨", async () => {
    const manager = new StorageAccessManager();
    const accessor = manager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_X,
    });
    await accessor.requestAccess({ namespace: NAMESPACE });
    await accessor.set(KEY, "gen-0");

    accessor.navigate();
    const stale = await accessor.set(KEY, "gen-1-attempt");
    assert.equal(stale.status, "denied-stale");

    await accessor.requestAccess({ namespace: NAMESPACE });
    const fresh = await accessor.set(KEY, "gen-1");
    assert.equal(fresh.ok, true);

    const generations = manager.ledger.map((record) => record.enqueuedGeneration);
    assert.deepEqual(generations, [0, 1, 1]);
    const statuses = manager.ledger.map((record) => record.status);
    assert.deepEqual(statuses, ["committed", "denied-stale", "committed"]);

    const read = await accessor.get(KEY);
    assert.equal(read.value, "gen-1");
  });

  it("导航切换顶层站点后，旧授权以 context/stale 规则确定失败", async () => {
    const manager = new StorageAccessManager();
    const accessor = manager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_X,
    });
    await accessor.requestAccess({ namespace: NAMESPACE });

    accessor.navigate({ topLevelSite: SITE_B });
    const result = await accessor.get(KEY);
    assert.equal(result.ok, false);
    // 顶层站点身份不匹配在入队时即确定，优先归为 context。
    assert.equal(result.status, "denied-context");
    assert.equal(accessor.documentGeneration, 1);
    assert.equal(accessor.topLevelSite, SITE_B);
  });
});

describe("并行写入的顺序确定性", () => {
  it("同一访问器上的并行写按入队顺序串行落定", async () => {
    const manager = new StorageAccessManager();
    const accessor = manager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_X,
    });
    await accessor.requestAccess({ namespace: NAMESPACE });

    const results = await Promise.all([
      accessor.set(KEY, "first"),
      accessor.set(KEY, "second"),
      accessor.set(KEY, "third"),
    ]);
    assert.ok(results.every((result) => result.ok));

    const finalRead = await accessor.get(KEY);
    assert.equal(finalRead.value, "third");

    const writes = manager.ledger.filter((record) => record.operation === "set");
    assert.equal(writes.length, 3);
    assert.ok(writes.every((record) => record.status === "committed"));
  });

  it("授权撤销对在途与后续事务产生确定的拒绝归属", async () => {
    const gate = new GateBackend();
    const manager = new StorageAccessManager({ unpartitioned: gate });
    const accessor = manager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_X,
    });
    const grant = await accessor.requestAccess({ namespace: NAMESPACE });

    gate.arm();
    const head = accessor.set("head-key", "head-value");
    await gate.waitUntilBlocked();

    const inFlight = accessor.set(KEY, "will-be-revoked");

    // 目标事务在队列中等待期间撤销授权。
    const revoked = manager.revokeGrant(grant.grantId);
    assert.equal(revoked, true);
    gate.release();

    await head;
    const result = await inFlight;
    assert.equal(result.ok, false);
    assert.equal(result.status, "denied-revoked");

    const record = manager.ledger.at(-1)!;
    assert.equal(record.grantId, grant.grantId);
    assert.equal(record.enqueuedGeneration, record.executedGeneration);
    assert.equal(
      await gate.get(namespacedKey(NAMESPACE, KEY)),
      null,
    );
  });
});

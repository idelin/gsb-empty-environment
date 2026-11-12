import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  StorageAccessManager,
  namespacedKey,
} from "../src/storage-access.ts";
import { MinimalWebStorageBackend } from "../src/backends.ts";
import {
  EMBED_X,
  EMBED_Y,
  FakeWebStorage,
  KEY,
  NAMESPACE,
  SITE_A,
  SITE_B,
  countingPrompt,
} from "./helpers.ts";

describe("命名空间隔离与同名键", () => {
  it("分区缓存与未分区命名空间的同名键互不污染", async () => {
    const manager = new StorageAccessManager();
    const accessor = manager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_X,
    });

    await accessor.setPartitioned(KEY, "partition-secret");
    const grant = await accessor.requestAccess({ namespace: NAMESPACE });
    await accessor.set(KEY, "unpartition-secret");

    const partitioned = await accessor.getPartitioned(KEY);
    const authorized = await accessor.get(KEY);

    assert.equal(partitioned, "partition-secret");
    assert.equal(authorized.ok, true);
    assert.equal(authorized.value, "unpartition-secret");

    // 未分区后端里只存在带命名空间前缀的键，原始同名键不会泄漏。
    const backend = manager.unpartitionedBackend;
    assert.equal(await backend.has(namespacedKey(NAMESPACE, KEY)), true);
    assert.equal(await backend.has(KEY), false);

    // 授权对象冻结了授权时刻的身份与命名空间。
    assert.equal(grant.namespace, NAMESPACE);
    assert.equal(grant.topLevelSite, SITE_A);
    assert.equal(grant.embedderOrigin, EMBED_X);
    assert.equal(grant.documentGeneration, 0);
  });

  it("不同命名空间的同名键保持独立", async () => {
    const manager = new StorageAccessManager();
    const accessor = manager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_X,
    });

    const first = await accessor.requestAccess({ namespace: "ns-a" });
    await accessor.set(KEY, "a-value");
    manager.revokeGrant(first.grantId);

    const second = await accessor.requestAccess({ namespace: "ns-b" });
    await accessor.set(KEY, "b-value");

    const read = await accessor.get(KEY);
    assert.equal(read.value, "b-value");
    assert.equal(
      await manager.unpartitionedBackend.get(namespacedKey("ns-a", KEY)),
      "a-value",
    );
    assert.equal(
      await manager.unpartitionedBackend.get(namespacedKey("ns-b", KEY)),
      "b-value",
    );
    assert.equal(second.namespace, "ns-b");
  });

  it("授权绑定顶层站点与嵌入来源，换站点后的授权不可用", async () => {
    const manager = new StorageAccessManager();
    const accessor = manager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_X,
    });
    await accessor.requestAccess({ namespace: NAMESPACE });
    await accessor.set(KEY, "site-a-secret");

    // 导航到另一个顶层站点：文档代前进，旧授权不再可写。
    accessor.navigate({ topLevelSite: SITE_B });
    const staleWrite = await accessor.set(KEY, "site-b-attack");
    assert.equal(staleWrite.ok, false);
    assert.equal(staleWrite.status, "denied-context");

    // 重新在新站点授权后才能写，且原值未被覆盖。
    await accessor.requestAccess({ namespace: NAMESPACE });
    const write = await accessor.set(KEY, "site-b-secret");
    assert.equal(write.ok, true);
    const read = await accessor.get(KEY);
    assert.equal(read.value, "site-b-secret");
  });

  it("不同嵌入来源即使在同一顶层站点也获得独立授权", async () => {
    const manager = new StorageAccessManager();
    const first = manager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_X,
    });
    const second = manager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_Y,
    });

    await first.requestAccess({ namespace: NAMESPACE });
    await first.set(KEY, "from-x");

    assert.equal(second.boundGrant, null);
    const denied = await second.get(KEY);
    assert.equal(denied.ok, false);
    assert.equal(denied.status, "denied-no-grant");
  });

  it("撤销不会把未分区值复制回分区缓存", async () => {
    const manager = new StorageAccessManager();
    const accessor = manager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_X,
    });
    await accessor.setPartitioned(KEY, "partition-value");

    const grant = await accessor.requestAccess({ namespace: NAMESPACE });
    await accessor.set(KEY, "unpartition-value");

    const revoked = accessor.revokeAccess();
    assert.equal(revoked, true);
    assert.equal(accessor.boundGrant, null);
    assert.equal(manager.getGrant(grant.grantId)?.revoked, true);

    // 分区缓存仍是原值，未分区值保持在原位，没有发生任何复制/迁移。
    assert.equal(await accessor.getPartitioned(KEY), "partition-value");
    assert.equal(
      await manager.unpartitionedBackend.get(namespacedKey(NAMESPACE, KEY)),
      "unpartition-value",
    );

    const afterRevoke = await accessor.get(KEY);
    assert.equal(afterRevoke.ok, false);
    assert.equal(afterRevoke.status, "denied-no-grant");
  });

  it("撤销后换命名空间再授权读不到旧命名空间的数据", async () => {
    const manager = new StorageAccessManager();
    const accessor = manager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_X,
    });
    const first = await accessor.requestAccess({ namespace: "ns-a" });
    await accessor.set(KEY, "secret-a");
    accessor.revokeAccess();

    await accessor.requestAccess({ namespace: "ns-b" });
    const read = await accessor.get(KEY);
    assert.equal(read.ok, true);
    assert.equal(read.value, null);
    assert.equal(manager.getGrant(first.grantId)?.revoked, true);
  });

  it("只读授权允许读取但拒绝写操作", async () => {
    const manager = new StorageAccessManager();
    const writer = manager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_X,
    });
    await writer.requestAccess({ namespace: NAMESPACE });
    await writer.set(KEY, "seed");
    writer.revokeAccess();

    const reader = manager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_X,
    });
    await reader.requestAccess({ namespace: NAMESPACE, rule: "read-only" });

    const read = await reader.get(KEY);
    assert.equal(read.ok, true);
    assert.equal(read.value, "seed");

    const write = await reader.set(KEY, "tamper");
    assert.equal(write.ok, false);
    assert.equal(write.status, "denied-read-only");

    const remove = await reader.remove(KEY);
    assert.equal(remove.ok, false);
    assert.equal(remove.status, "denied-read-only");
  });

  it("用户拒绝授权时抛出异常且不产生授权", async () => {
    const prompt = countingPrompt(false);
    const manager = new StorageAccessManager({ prompt: prompt.handler });
    const accessor = manager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_X,
    });

    await assert.rejects(
      () => accessor.requestAccess({ namespace: NAMESPACE }),
      /存储访问被拒绝/,
    );
    assert.equal(prompt.calls.length, 1);
    assert.equal(manager.listGrants().length, 0);
    assert.equal(accessor.boundGrant, null);
  });

  it("可通过 Web Storage 适配器承载未分区后端", async () => {
    const webStorage = new FakeWebStorage();
    const manager = new StorageAccessManager({
      unpartitioned: new MinimalWebStorageBackend(webStorage),
    });
    const accessor = manager.createAccessor({
      topLevelSite: SITE_A,
      embedderOrigin: EMBED_X,
    });
    await accessor.requestAccess({ namespace: NAMESPACE });
    await accessor.set(KEY, "web-secret");

    const read = await accessor.get(KEY);
    assert.equal(read.value, "web-secret");
    assert.equal(webStorage.getItem(namespacedKey(NAMESPACE, KEY)), "web-secret");
  });
});

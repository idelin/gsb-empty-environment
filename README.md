# 浏览器存储分区迁移

嵌入文档获得未分区存储访问授权（Storage Access 模型）时，本组件保证两个
命名空间的秘密不会被直接合并。

## 安全模型

- **授权三元组绑定**：每条授权在签发时冻结顶层站点、嵌入来源与文档代
  （`topLevelSite` / `embedderOrigin` / `documentGeneration`），并固定一个
  命名空间；之后的读写只落在 `ns:<namespace>::<key>` 下。
- **命名空间物理隔离**：分区缓存与未分区后端是两个独立 `KvBackend`，
  同名键互不污染；组件不提供任何合并或迁移 API。
- **撤销零复制**：`revokeGrant` / `revokeAccess` 只翻转授权状态，不读取、
  不复制、不删除后端数据，未分区值不会被搬回分区缓存。
- **旧事务确定归属**：身份三元组与只读规则在入队时刻冻结；文档代导航与
  撤销在出队执行时刻重评，因此在途事务得到 `denied-stale` / `denied-revoked`
  等确定结果，全部写入审计账本 `manager.ledger`。
- **并行授权去重**：相同三元组 + 命名空间 + 规则的并行请求共享一个
  in-flight Promise，用户提示只触发一次。

## 用法

```ts
import { StorageAccessManager } from "./index.ts";
import { WebStorageBackend } from "./index.ts";

const manager = new StorageAccessManager({
  unpartitioned: new WebStorageBackend(window.localStorage),
});

const accessor = manager.createAccessor({
  topLevelSite: "https://news.example",
  embedderOrigin: "https://widget.example",
});

// 未授权：只碰分区缓存
await accessor.setPartitioned("token", "partition-secret");

// 授权：命名空间在此时固定，后续读写始终使用它
await accessor.requestAccess({ namespace: "user-vault" });
await accessor.set("token", "unpartition-secret");

// 导航使文档代前进，旧授权的在途/后续事务被拒绝
accessor.navigate();

// 撤销：数据保持原位，不发生任何复制
accessor.revokeAccess();
```

## 测试

```sh
npm test
```

覆盖并行授权去重、导航后旧事务归属、撤销在途拦截、同名键跨命名空间隔离、
只读授权、拒绝授权与 Web Storage 适配，共 17 个用例。

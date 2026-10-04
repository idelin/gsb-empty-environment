# columnar-pipeline

一个有限的列式查询管道（TypeScript，零依赖，不依赖数据库）：
先筛选行号、再按需读取宽字段（late materialization）。

## 语义

- 操作只有 `filter`、`project` 和按单键的稳定 `sort`。
- 三值逻辑：任何含 `null` 的比较结果为 `unknown`；`filter` 只保留谓词为 `true` 的行。
- 排序单键稳定：同键（含 null 键，null 排在最后且互为同键）按原始输入次序；
  每一行在扫描阶段就打上全局到达序号 `seq`，次序在加载任何宽列**之前**就已确定，
  延迟读取不可能引起重排。
- 行有稳定 `rowId` 与每行 `rowVersion`；同一 `rowId` 的不同版本是互不相同的行。

## 批次物理布局

```
ColumnBatch  (batchId, data version, slot s, slot generation g)
├─ rowCount
├─ rowIds:       number[]          稳定 rowID
├─ rowVersions:  number[]          每行版本
├─ narrow cols:  { values: T[], nulls: Uint8Array }   内联在批次缓冲里
└─ wide cols:    只有 (name, loader) 句柄；数据按需异步加载，
                 成功后在批次生命周期内缓存，释放时丢弃
```

`filter` 只接受窄列，输出选择向量 `SelectionVector`（`Uint32Array` 行号 + count）。
选择向量绑定批次身份（`batchId` + 数据 `version`），每次 `resolve()` 都重新校验活性。

## 生命周期

- 批次状态机：`open -> released`。
- `BatchSource` 把批次发射进固定数量的物理槽位；槽位复用时旧批次被 `release`，
  槽位 `generation` 递增。旧视图/旧选择向量再次使用会抛
  `BatchReleasedError` / `StaleViewError`——上一批的行号永远不会被映射到新数据上。
- 执行器在查询期间 `pin()` 住仍需要的批次；被 pin 的槽位不能被复用
  （`PoolExhaustedError`），查询结束在 `finally` 里 `unpin()`。
- 宽列加载是异步且可取消的：`loadWide(name, { signal })`，
  取消抛 `LoadCancelledError`。

## 载入统计

`source.stats` 统计：批次 `batchesOpened/Released/Recycled`，行
`rowsScanned/Selected/Emitted`，以及每个宽列的
`started/completed/cancelled/failed` 次数。未投影的宽列不会出现任何计数——
它从未被读取（测试里同时用加载回调计数双重验证）。

## 运行

需要任意 `tsc`（>= 3.9）。Node 16+。

```sh
tsc -p tsconfig.json          # 编译到 dist/
node dist/test/run.js         # 测试（自带极简 runner，无依赖）
node dist/examples/demo.js    # 可运行示例
```

测试覆盖：空选择、全 null、跨批稳定排序、重复 rowID 不同版本、
取消、缓冲复用、pin 保护、宽列谓词拒绝，以及"管道输出与逐行急切参考
实现逐值相同"。

# tombstone-lsm

用 Go 标准库实现的最小 LSM 树删除语义内核:**Tombstone(墓碑)标记 + 生命周期清理器**。

## 设计

- **删除即写入**:`Delete(key)` 不原地移除数据,而是写入一条 `Tombstone=true` 的 Entry(携带全局递增 `Seq`),与普通写一样经 Memtable → Flush 落盘为 SSTable。
- **读路径屏蔽**:
  - Point Get 按 Memtable → L0(新到旧)→ L1 … 的顺序查找,命中的最新记录若是 Tombstone,立即返回 `ErrNotFound`;
  - Range Scan 对所有数据源做 k 路归并,每个 Key 只保留最新版本,Tombstone 版本被跳过,旧版本被去重。
- **生命周期清理(Compaction)**:每层按条目数水位触发合并(`Level0Capacity * LevelSizeRatio^level`),把超容层整体并入下一层:
  - 目标层**不是** Bottom-most:更深层可能仍有被屏蔽的旧版本,Tombstone 必须保留并随合并下沉;
  - 目标层**是** Bottom-most:之下再无数据,Tombstone 使命完成,连同被它屏蔽的旧版本一起**物理擦除**,释放空间。

## 目录

- `lsm/` — 内核包(Memtable / SSTable / 归并迭代器 / DB / Compaction)
- `cmd/demo/` — 运行样例,演示 Tombstone 从写入到物理擦除的全过程
- `lsm/lsm_test.go` — 验收测试(`go test`)

## 运行

```sh
go test ./lsm/ -v        # 运行验收测试
go run ./cmd/demo        # 运行演示(临时数据写入 ./demo-data,退出时清理)
```

## 验收边界对应

1. 插入 `K1=V1` 后 `Delete(K1)` 写入 Tombstone → `TestDeleteWritesTombstoneAndGetReturnsNotFound`
2. 查询 `K1` 立即返回 NotFound(Memtable / SSTable 两种情形)→ 测试 1、2
3. 多轮 Compaction 下沉至最后一层且无更老版本后彻底擦除 → `TestTombstonePurgedAtBottomMostLevel`(含磁盘字节级验证)

## 说明

- 仅使用标准库,兼容 Go 1.11+。
- 未实现 WAL:未 Flush 的 Memtable 数据在进程崩溃时丢失(`Close` 会自动 Flush)。

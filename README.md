# pagezip

页压缩库：多个页共享并按需切换字典，每个页头绑定字典的**精确版本摘要**
（ID + 内容 SHA-256）。恢复与回收只依据页头与引用账本，绝不拿"最新字典"猜解。

## 布局

```
<dir>/
  <dictID>.<sha256hex>.dict   # 字典版本，内容寻址；同 ID 不同内容 => 不同文件
  <pageID>.page               # 页：magic | dictID | digest | payload | CRC32
  ledger.log                  # 追加式引用账本（JSON Lines）
```

账本操作：`dict_add` / `dict_del` / `setref` / `delpage`。

## 写入与切换顺序

1. `PutDict`：先落盘字典文件（原子写），再追加 `dict_add`。
2. `WritePage`：先原子提交页文件（tmp + rename，**页文件即提交点**），
   再追加 `setref` 并更新内存引用计数。

崩溃恢复（`Open`）= 重放账本 + 用磁盘页头对账：

- 已提交页头覆盖账本（切换在页提交后、账本追加前中断 → 以页头为准）。
- 账本引用指向已不存在的页文件 → 丢弃该引用。
- 页文件存在但已损坏 → 保留账本中的引用（回收保持保守，绝不提前释放）。
- 账本尾部半行（撕裂写）→ 截断容忍。

## 回收

- `DeleteDict`：引用计数 > 0 时返回 `ErrDictInUse` —— 旧页未改写前不能删旧字典。
- `Reclaim`：只删除引用计数为 0 的字典版本，返回回收清单。

## 错误分类

| 情况 | 类型 |
|---|---|
| 页头绑定的精确字典版本缺失 | `*DictMissingError` |
| 字典内容哈希与摘要不符 | `*DictCorruptError` |
| 页文件结构 / CRC / 解压失败 | `*PageCorruptError` |

判定辅助：`IsDictMissing` / `IsDictCorrupt` / `IsPageCorrupt`。

## 独立重放

`ReplayLedger(r, headers, present)` 与 `ScanPageHeaders(dir)` 均为导出函数，
可脱离活跃 Store 独立重放引用账本，审计回收决策（见
`TestIndependentLedgerReplay`）。

## 测试

```sh
GO111MODULE=on go test ./...
```

覆盖：字典切换中断（提交点前/后）、同字典 ID 不同内容、缺字典不回落最新、
坏字典 / 坏页分别报告、账本独立重放一致性、撕裂账本尾部。

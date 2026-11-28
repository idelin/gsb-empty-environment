# blink

`blink` 是一个可导入的 Go Lehman-Yao B-link Tree 并发索引引擎。键按字节序
（`bytes.Compare`）升序排列，页内使用 `RWMutex`，读路径每次只持有**一个页锁**。

```go
import blink "prescreen.local/ps-d9e293b8d7"

index := blink.New(64) // 每叶最多 64 个键
index.Insert([]byte("k1"), []byte("v1"))
value, ok := index.Get([]byte("k1"))
entries := index.Scan([]byte("a"), []byte("z")) // [start, end] 双向包含
old, deleted := index.Delete([]byte("k1"))
```

## 结构不变量

- 每个节点有 `highKey`（包含式上界，`nil` 表示 `+inf`）和 `rightLink`。
- 除每层最右页外，查找满足 `key > highKey` 时沿 `rightLink` 追赶，绝不返回假阴性。
- 页分裂先创建右兄弟、迁移右半数据与旧的 `highKey/rightLink`，再发布左页新的
  `highKey/rightLink`。父页指针尚未更新时，整棵树已经可并发读取。
- 父层安装按自底向上推进；父页若也被其他分支分裂，查找父页时同样通过右链
  `move-right` 定位。
- 根分裂通过原子 CAS 切换 `rootPageID`；旧根保留右链，已经进入旧根的读者仍可
  安全到达目标叶页。
- 每个节点的分裂由页级 `splitMu` 覆盖“分裂 -> 父指针安装”窗口，同一页不会在旧
  分裂上传前再次分裂；不同子树的分裂可并行，没有全局分裂锁。

## 并发范围扫描

`Scan` / `Range` 沿叶层 `rightLink` 顺序推进，每次只锁一个叶页。并发分裂只会把
已复制过的键移动到右侧兄弟页；扫描按 `lastKey` 去重，因此不会重复输出，也不会
漏掉扫描期间已稳定存在的键。`nil` 边界表示无穷：

```go
all := index.Scan(nil, nil)
err := index.Range([]byte("b"), []byte("d"), func(key, value []byte) bool {
    // 回调执行时不持有页锁，可以停止或读取树。
    return true
})
```

范围扫描是并发安全的，但不是跨页 MVCC 快照：扫描开始后、已经越过的页上新增的键
可能不出现在本次结果中。分裂搬移的键、范围边界内的稳定键不会丢失。

## 删除与回收

删除只移出键值；空叶页在“左兄弟可继承其范围且右邻已出现在父页”时被惰性摘除。
被摘除页标记为 `dead`，持有旧指针的读者会从根重试或沿右链跳过，避免把写操作
落到已脱离父页的页上。也可以显式调用 `ReclaimEmpty()` 做一轮回收。

## 主要 API

| API | 说明 |
| --- | --- |
| `New(maxKeys ...int)` | 创建默认配置的树 |
| `NewWithOptions(Options)` | 配置叶容量、内部扇出、分裂测试钩子 |
| `Insert` / `Put` / `Store` | 插入或更新，返回是否为新键 |
| `Get` / `Lookup` / `Contains` | 单页锁点查 |
| `Delete` / `Remove` | 删除并返回旧值 |
| `DeleteKey` | 删除并只返回布尔值 |
| `Scan` / `Snapshot` / `RangeEntries` | 返回 `[start,end]` 的 `[]Entry` |
| `Range` / `Ascend` | 回调式有序遍历 |
| `Len` / `Count` / `Stats` / `Height` / `RootPageID` | 计数与诊断 |
| `Validate` / `Check` | 静止状态下的结构校验器 |
| `ReclaimEmpty` | 主动回收空叶页 |
| `Close` | 生命周期占位，无外部资源 |

`Options.AfterSplit` 在安全半分裂完成、页数据锁释放之后、父指针安装之前运行，
用于注入“半分裂延迟”。此时仍持有左右页的页级 split latch，因此回调必须只读，
不得触发新的分裂。

## 验证

```bash
gofmt -w .
go vet ./...
go test ./...
go test -race -count=10 ./...
```

测试覆盖：

- 高并发混合插入、点查与范围扫描，检查零假阴性、零重复、严格有序。
- 半分裂延迟时通过 `move-right` 命中尚未挂入父页的右兄弟。
- 并发多层根分裂与结构校验。
- 并发删除、空页回收与扫描并行运行。
- `-race` 下零数据竞争、零死锁（测试超时也会失败）。

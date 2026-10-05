# overflow

溢出页回收库：把任意大小的记录存进定长页（256 B）组成的数据文件，
超过单页的记录拆成溢出页链。根引用（记录名 → 链首页号）存放在
配套的目录文件 `<data>.dir` 中。

## 更新协议（崩溃安全）

1. 从回收集或文件末尾分配新页；
2. 写完整条新链并 fsync 数据文件；
3. 原子切换根引用（写临时目录文件 + rename）；
4. 旧链此刻才变为不可达，进入回收集。

根切换之前的任何写失败都会回滚内存分配，旧记录完好；进程在根切换前
崩溃留下的孤儿链，会在下次 `Open` 或 `Collect` 时被回收——回收只删
不可达页，绝不触碰从任一活根可达的页。

## 坏结构检测

`Open` / `Verify` / `Get` 遍历链时，以下情况返回 `*CorruptError`：

- 链有环（页号在单链内重复）；
- 页被重复引用（同一页出现在多条链中）；
- 根或 next 指针越界、页魔数损坏。

## 对账

`Verify` 返回 `Ledger{Pages, Used, Free, Records, Digest}`：总页账
（Used + Free + 1 超级块 = Pages）与全部记录的 SHA-256 内容摘要。
测试中的独立遍历直接解析原始文件字节复核该总账与摘要。

## 用法

```go
s, err := overflow.Open("data.ovf")
if err != nil { ... }
defer s.Close()

err = s.Put("record", bigPayload)   // 空载荷 = 删除并回收
data, err := s.Get("record")
ledger, err := s.Verify()           // 全量遍历对账
n, err := s.Collect()               // 回收不可达页
```

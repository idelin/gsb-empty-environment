# DNS别名否定响应组合

安全解析器沿CNAME追踪时收到否定回答，不能把末端不存在套到起始名称的所有类型。写供Go解析宿主调用的应答组合包，各跳记录查询类型、权威区和有效期，拒绝循环及相互矛盾的别名，否定结论只覆盖证明的末端。用脚本解析器测试跨区别名、同名不同类型和途中超时，返回完整信任路径。

## 设计

- `Composer` 在 `Resolver` 接口之上做 CNAME 追踪，`Resolve(name, qtype)`
  返回 `Result`：`Hops` 逐跳记录查询类型、权威区、TTL 与绝对失效时刻，
  `TrustPath()` 给出从起始名到末端的完整名字序列。
- 循环检测：链中再次出现已访问名字即返回 `LoopError`。
- 矛盾检测：同一名字在 TTL 有效期内指向不同目标、单应答内出现多条不同
  CNAME、或 CNAME 与同类型数据共存，均返回 `ContradictionError`。
- 否定作用域：
  - `NODATA`（NOERROR 无数据）只否定 `(末端名字, 查询类型)`，不覆盖该名字的
    其它类型，更不覆盖起始名字；
  - `NXDOMAIN` 只覆盖被证明名字本身及其下属名字（见 `NegativeProof.Covers`），
    绝不向上套到把它引到这里的起始名字。
- 途中超时/网络错误返回 `IncompleteChainError`，携带 `PartialPath` 部分信任
  路径且不合成任何否定结论；`SERVFAIL` 等也不会被当成可缓存否定。

## 文件

- `types.go`、`names.go`：记录类型、否定结论与名字规范化。
- `composer.go`：追踪、循环/矛盾拒绝、TTL 与否定作用域组合。
- `errors.go`：`LoopError`、`ContradictionError`、`IncompleteChainError` 等。
- `scripted.go`：内存脚本解析器，可编排应答与超时，供宿主测试复用。
- `composer_test.go`：跨区别名、同名不同类型、途中超时及各类安全分支。
- `cmd/trustpath-demo`：打印三种场景完整信任路径的可运行演示。

## 运行

```sh
go test ./... -race
go run ./cmd/trustpath-demo
```

# strdict

对象批次共享字符串字典:引用与内联混合编码、按版本冻结的分代字典、
带字节预算的流式解码。仅使用 Go 标准库。

## 序列化范围(自定义,纯数据)

```
stream := frame*
frame  := tag payload
tag    := 0x00                      ; 流结束标记
        | 0x01 uvarint n, n 字节    ; 内联字符串,n <= MaxInlineLen (16 MiB)
        | 0x02 uvarint idx          ; 引用当前字典代中的条目
        | 0x03 uvarint version      ; 显式字典版本切换点
```

- 解码不执行任何宿主代码,帧只承载字符串数据与索引。
- 先验证长度再分配:内联帧声明长度先过 `MaxInlineLen` 与剩余预算检查,
  通过后才分配暂存缓冲。
- 字节预算 = 字典字节(每次绑定一代计入)+ 暂存/展开输出字节;
  流式解码超预算即拒绝扩张(`ErrBudgetExceeded`)。
- 空串是合法池项;未知索引返回 `ErrUnknownIndex`,绝不当作空串。
- 切换版本后旧引用不复用:同一索引按新代解释,切换点之前的引用
  仍按旧代解释。

## 字典生命周期

1. `NewDictionary(version)` → `Intern`(可入池空串,哈希碰撞按桶内
   字符串比较解决,哈希函数可注入以强制碰撞)→ `Freeze` 后不可变。
2. `Registry.Publish` 发布冻结代;`Acquire` 取得读租约。
3. `Registry.Reclaim` 仅在无活跃租约时回收该代;回收后再解码该版本
   得到 `ErrUnknownVersion`。
4. `Registry.BeginBatch` 将多个租约编组,`Batch.Cancel` 批量取消并
   释放全部租约;被取消会话的解码以 `ErrLeaseReleased` 失败。

## 解码会话

- `NewDecoder(r, DecoderConfig{Registry, Version, Budget, Batch})`
  将会话固定到初始版本;流中 0x03 帧显式切换。
- `Checkpoint()` 记录 {字节偏移, 当前版本},可从该点用新会话恢复解码。

## 运行

```sh
go test ./...        # 全部测试(含 Example 运行样例)
go test -run Example -v
```

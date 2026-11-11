# xmlentity

文档导入接口接受带内部DTD的XML，几KB的嵌套实体引用能展开成数GB。给Go导入服务实现放在encoding/xml解码器前面的实体展开读取器：只允许内部通用实体，拒绝外部实体、参数实体和递归引用；按展开后总字节、单个实体被引用次数和嵌套深度设预算，超限立即报错，不先缓冲全文。用指数膨胀样本与正常文档对比内存峰值和输出。

## 实现

`NewReader(r io.Reader, limits Limits) *Reader` 返回一个实现 `io.Reader` 的流式
实体展开读取器，可直接传给 `xml.NewDecoder`：

```go
dec := xml.NewDecoder(xmlentity.NewReader(body, xmlentity.Limits{
    MaxExpandedBytes:   16 << 20, // 展开后总字节
    MaxRefsPerEntity:   10_000,   // 单实体引用次数
    MaxTotalEntityRefs: 100_000,  // 全文实体引用总数
    MaxNestDepth:       32,       // 实体嵌套深度
    MaxDTDBytes:        1 << 20,  // 内部 DTD 子集大小
}))
```

### 安全策略

- 只接受内部 DTD 子集中的内部通用实体 `<!ENTITY name "value">`。
- 外部 DTD / 外部实体（`SYSTEM`、`PUBLIC`）、参数实体（`%name;`、`<!ENTITY % ...>`）、
  未解析实体（`NDATA`）、`NOTATION` 一律返回 `ErrExternalEntity`。
- 直接或间接递归的实体引用返回 `ErrRecursiveEntity`（基于活动实体栈判定）。
- 三重预算：展开总字节 `LimitExpandedBytes`、单实体引用次数 `LimitRefsPerEntity`、
  嵌套深度 `LimitNestDepth`；另加全文引用总数与 DTD 大小预算。
  超限返回包装 `ErrLimitExceeded` 的 `*LimitError`，可通过 `errors.As` 区分种类。
- 预定义实体（`lt/gt/amp/quot/apos`）与字符数字引用（`&#...;`）原样透传，
  交给 `encoding/xml` 解码；未声明实体也原样透传，由下游报标准 XML 错误。

### 流式保证

- 状态机逐字节驱动，实体替换文本通过有界的展开帧栈即时输出，不缓冲整份文档。
- 输出只经过固定的小块队列（`outPending`/`pushback`），底层数组跨 `Read` 复用。
- 预算在字节“产出”瞬间计费，超限立即终止，不会先把展开结果写入内存。

## 验证

- `go test ./...`：功能、拒绝策略、预算、递归、`encoding/xml` 集成与边界用例。
- `xmlentity_bomb_test.go`：28 层指数炸弹（751 字节输入，理论展开约 2.7 GB）
  在 KB 级输出与堆增长内被预算拦截；同体量正常文档完整展开、输出逐字节正确，
  两者内存峰值均为常数级，测试日志直接打印对比数据。
- `go test -bench .`：正常文档与炸弹快速失败路径的吞吐和每操作分配。

# Hessian2类定义白名单预检

RPC 服务用 Hessian2 接收对象，攻击者在类定义里引用白名单外的类，或用引用编号把深层对象图拼成环。为Go网关实现供RPC入口调用的 Hessian2 流预检包：识别类定义 C、对象 O 与紧凑对象字节 0x60 至 0x6f，类名须在调用方白名单内；引用 0x51 的编号必须指向已出现的对象，嵌套深度、对象总数和列表映射的声明长度超预算即拒；遇到不认识的类型字节停止并拒绝。用白名单外类名、越界引用、超长声明列表和合法嵌套对象验证。

## 用法

在把请求体交给 Hessian2 反序列化器之前，RPC 入口先调用 `Inspect`：

```go
policy := netsec.DefaultPolicy(map[string]struct{}{
	"com.example.Account": {},
})
if err := netsec.Inspect(req.Body, policy); err != nil {
	return // 拒绝该请求，err 可通过 errors.Is 分类
}
```

`Policy` 暴露三个预算，零值时回落到内置默认值：

- `MaxDepth`（默认 64）：嵌套容器与对象的最大深度。
- `MaxObjects`（默认 65536）：类实例（C/O/0x60-0x6f）总数上限。
- `MaxContainer`（默认 1<<20）：定长列表、映射声明长度及变长元素/条目数上限。

白名单为 nil 或空时按 fail-closed 处理，任何类定义都会被拒绝。

## 预检规则

- 识别类定义 `C`(0x43)、命名对象 `O`(0x4f) 与紧凑对象 `0x60`-`0x6f`；类名必须命中调用方白名单，紧凑/命名对象引用的类定义编号必须已存在。
- 引用 `Q`(0x51) 读取 24 位编号，编号必须小于当前已出现的可引用值（对象、列表、映射）数量；对象在解析其字段前即登记，因此指向自身或祖先的合法回环可以通过，越界编号拒绝。
- 定长列表/映射的声明长度、变长容器的实际元素/条目数、类字段数都受 `MaxContainer` 约束。
- 不认识或保留的类型字节立即停止并返回 `ErrUnknownType`；截断的流返回 `ErrMalformedStream`；流末尾的多余字节同样拒绝。

所有拒绝原因都包装在哨兵错误上，可用 `errors.Is(err, netsec.ErrClassNotAllowed)` 等方式判定：
`ErrUnknownType`、`ErrClassNotAllowed`、`ErrBadReference`、`ErrDepthExceeded`、
`ErrObjectLimit`、`ErrLengthExceeded`、`ErrMalformedStream`、`ErrInvalidLength`、
`ErrClassIndex`。

## 测试

```sh
go test ./...
```

测试用手工编码的 Hessian2 字节流覆盖白名单外类名、越界引用、超长声明列表/映射、
合法嵌套与自回环对象、未知类型字节、深度与对象预算、截断流等场景。

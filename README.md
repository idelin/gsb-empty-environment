# 信息流降密边界

这是一个可嵌入表达式执行器的 Go 信息流标签检查包。核心不变量是：**公开输出
的标签必须为空**。秘密值只能通过携带可信授权的 `Declassify` 节点降密。

## 语义

- `Source` 取值范围为 `[0, MaxSources)`，`MaxSources == 16`；`Label` 是一个
  16 位来源集合。
- 常量是低标签；`Secret(s)` 携带 `{s}`；`Add` 合并两个操作数的标签。
- `If` 把条件标签加入分支的程序计数器（pc），所以嵌套条件的所有控制依赖都会
  累积。分支值即使来自常量或已降密值，也仍带有外围条件标签。
- `Let`/`Var` 是不可变局部变量，读取时合并绑定标签与当前 pc。
- `Output` 只在标签为空时发布；否则整个事务失败。

## 授权

`Registrar.Authorize(request, node, allowed)` 签发不可伪造的 `Authorization`：

- 绑定到 `request` 的指针身份，不能跨请求使用；
- 绑定到 `DeclassifyNode` 的不可变节点身份，不能换到新构造的节点上；
- `allowed` 只允许移除其中列出的来源；
- 只能使用一次，消费发生在整个请求事务提交时。

执行器会对授权做具体类型校验，外部包用嵌入/包装方式伪造的接口值不会被接受。
`Registrar` 是信任边界，应由服务端持有，不能暴露给不可信表达式构造者。

分支内降密的计算为：

```text
(valueLabel \ allowed) ∪ pc
```

因此授权即使覆盖外围条件来源，也不能洗掉外围控制依赖。

## 原子性

`Execute` 是事务式的：

- 所有公开输出先进入事务缓冲；任一节点失败则返回零值 `Result`，不会暴露部分输出。
- 授权只在成功路径的 `Registrar.commit` 中一次性标记消费；失败回滚不消费授权。
- 同一 `Request` 的执行由请求级互斥锁串行化，同一授权并发竞争时恰好一个事务成功。
- 不同 `Request` 可并行执行，只在授权注册器的短临界区相遇；跨请求授权直接拒绝。
- `AuditLog` 并发安全，成功事件在提交后成批写入；失败事务只记录回滚/拒绝事件，
  不会产生 `output_published` 事件。

## 示例

```go
req, _ := netsec.NewRequest(map[netsec.Source]int{0: 41})
reg := netsec.NewRegistrar()
exec := netsec.NewExecutor(reg, nil)

site := netsec.Declassify(netsec.Secret(0))
auth, _ := reg.Authorize(req, site, netsec.NewLabel(0))

res, err := exec.Execute(req, netsec.Output(site.WithAuthorization(auth)))
// err == nil, res.Outputs[0].Value == 41, res.Outputs[0].Label.IsEmpty()
```

## 测试

```sh
go test ./...
go test -race ./...
```

`executor_test.go` 覆盖分支内/分支合并后的嵌套条件泄露、授权只移除允许来源、
同事务/跨事务/跨请求/跨节点授权重用、包装授权与外来注册器伪造、失败回滚、
正常定点降密以及并发请求原子性。

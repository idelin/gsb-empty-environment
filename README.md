# CEF脚本绑定能力表

嵌入式浏览器把原生对象注入页面脚本环境后，子帧或about:blank页面可能继承调用权。为Go宿主的浏览器嵌入层实现可导入的绑定能力表：能力按有效来源和帧类型登记，about:blank、srcdoc、blob继承发起者来源，data及opaque来源默认无权；每次调用复核当前帧导航代次。用子帧、blob和导航后旧回调样例测试，越权调用不得触达原生函数。

## 模型

- `Origin`：有效来源。三元组来源按 `(scheme, host, port)` 比较；不透明来源（`OpaqueOrigin`）彼此永不相等，且默认无权。
- `ResolveEffectiveOrigin`：按导航请求计算文档有效来源。
  - `about:blank` / `about:srcdoc` 继承发起者来源，发起者缺失或不透明则结果不透明。
  - `blob:` 优先取 URL 内嵌的创建者来源（`blob:https://app.example/<uuid>`），内嵌来源缺失时回退到发起者来源。
  - `data:` 以及无法形成主机三元组的 URL 一律为不透明来源。
- `Frame`：主帧/子帧，持有当前有效来源与单调递增的导航代次 `Generation`，每次 `Navigate` 代次加一。
- `CapabilityTable`：能力按 `(binding, 有效来源, 帧类型)` 登记，拒绝向不透明来源授权。
- `Callback`：`Bind` 时固化帧 ID 与导航代次；每次 `Call` 复核帧、代次与当前授权，任一不匹配即在闸门处返回错误，原生函数不会被触达。

## 拒绝路径（哨兵错误）

- `ErrOpaqueOrigin`：data/opaque 来源调用或登记。
- `ErrCapabilityNotRegistered`：来源或帧类型未登记该绑定（含撤销后）。
- `ErrStaleGeneration`：导航后旧代次回调继续调用。
- `ErrFrameMismatch`：回调被拿到其它帧调用。

## 用法

```go
table := netsec.NewCapabilityTable()
app := netsec.NewTupleOrigin("https", "app.example.com", "")
_ = table.Grant("host.readFile", app, netsec.FrameSubframe, func(args []any) (any, error) {
    // 只有通过闸门的调用才会到达这里
    return "data", nil
})

frame := netsec.NewFrame("iframe-1", netsec.FrameSubframe)
frame.Navigate(netsec.NavigationRequest{URL: "about:blank", Initiator: app}) // 有效来源 = app

cb, err := table.Bind("host.readFile", frame)
if err != nil { /* 无权上下文，连回调都拿不到 */ }
if _, err := cb.Call(frame, nil); err != nil { /* 代次/来源复核失败 */ }

frame.Navigate(netsec.NavigationRequest{URL: "data:text/html,x", Initiator: app})
_, err = cb.Call(frame, nil) // errors.Is(err, ErrStaleGeneration)，原生函数不触达
```

## 测试

```sh
go test -race -count=1 ./...
```

覆盖：子帧跨来源/帧类型隔离、about:blank 与 srcdoc 继承、blob 创建者来源继承与回退、data/opaque 默认拒绝、导航后旧回调失效、回调跨帧拒绝、撤销即时生效，以及导航与调用并发的 race 场景；所有越权样例都断言原生函数调用计数保持不变。

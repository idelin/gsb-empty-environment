# netsec：闭包逃逸与栈槽复用分析

`example.org/netsec/q0072` 是一个可导入的 Go 包，用有限 SSA 图分析“闭包把栈槽 by-ref 捕获交给异步插件后，原帧退出、槽被其他租户复用”的悬垂引用问题。分析结果包含最短证据路径、缺失 `HeapPromote` 的路径，并可用配套解释器把报告路径实际重放为跨租户读取。

## 快速使用

```go
package main

import (
	"fmt"

	netsec "example.org/netsec/q0072"
)

func main() {
	b := netsec.NewBuilder()
	b.FrameTemplate("tenant-A", "tenant-A",
		netsec.FrameSlot{Tenant: "tenant-A", Data: "A-SECRET"})

	b.FrameAlloc("tenant-A", 1, netsec.WithID("alloc"))
	ref := b.CaptureByRefSlot("tenant-A", 0, true, netsec.WithID("capture"))
	closure := b.MakeClosureWith([]netsec.Capture{{
		Kind:   netsec.CaptureByRef,
		Source: ref.Result,
		Frame:  "tenant-A",
		Slot:   0,
	}}, netsec.WithID("closure"))
	b.PassAsync(closure.Result, netsec.WithID("async"))
	b.Return("", "tenant-A", netsec.WithID("return"))

	report, err := netsec.Analyze(b.Program)
	if err != nil {
		panic(err)
	}
	fmt.Println(report.Status) // vulnerable

	check, err := netsec.VerifyFinding(b.Program, report.Findings[0],
		netsec.ExecuteOptions{AttackerTenant: "tenant-B", AttackerData: "B-SECRET"})
	if err != nil {
		panic(err)
	}
	fmt.Println(check.Leaked, check.ObservedTenant, check.ObservedData)
	// true tenant-B B-SECRET
}
```

`Builder` 的普通操作会自动串成线性 CFG；分支使用 `Break`、`Op`、`Program.Link` 手工连接，合并点用 `Phi` 保留来源集合。

## 值域与操作

值类型：

- `Scalar`：租户数据。`CaptureByValue` 复制它。
- `FrameRef{Frame, Slot, Mutable, CallSite}`：栈槽引用。`CaptureByRef` 在未提升时保留它。
- `HeapCell`：一次 `HeapPromote` 为某个 `FrameRef` 分配的堆单元。
- `Closure`：`MakeClosure` 的环境，可递归包含嵌套闭包。

操作：

- `FrameAlloc`、`CaptureByRef`、`CaptureByValue`、`MakeClosure`
- `HeapPromote`、`StoreGlobal`、`PassAsync`、`CallNow`、`Return`
- `StoreHeap`：把闭包写入已有 `HeapCell`。
- `Scalar`、`Phi`、`Branch`：标量常量、SSA 合流和控制流合并节点。

`HeapPromote` 的 `Cell` 未显式给出时使用提升点的稳定 ID。包会拒绝同一个 `HeapCell` 被两个不同 `FrameRef` 共用。提升会改写当前路径上所有指向同一 `Location` 的已知 SSA 别名，并使提升之后新建的 `CaptureByRef` 直接得到该 `HeapCell`。

## 判定规则

闭包流入以下任一点即视为逃逸：

1. `StoreGlobal`
2. `PassAsync`
3. 作为 `Return` 值
4. `StoreHeap` 写入 `HeapCell`

`CallNow` 只做同步调用，不是逃逸点。

对每个逃逸闭包，分析递归检查其 by-ref 捕获。若捕获仍可能是 `FrameRef`，该捕获必须在逃逸点之前被 `HeapPromote` 覆盖；合流后的状态会同时保留“已提升到 cell A”和“未提升”的可能，因此只提升分支一侧不会被误判为安全。捕获发生时帧已经 `Return` 会报告 `capture-after-return`。

数据流在合流处保留所有来源：

- 值来源集合 `Origins` 会合并，并在报告中列出。
- 提升状态按 `Location{Frame, CallSite, Slot}` 合并。
- `CallSite` 与 `FrameID` 一起区分递归调用的不同实例。
- 分析预算耗尽时 `Status` 为 `unknown`，并给出 `Unknowns`；不会把不完整结果报成安全。

## 报告

`Analyze` 返回 `*Report`：

- `Status`：`safe`、`vulnerable` 或 `unknown`。
- `Findings`：每个不安全捕获对应一条，包含 `Sink`、`Closure`、捕获位置和 `Reason`。
- `Finding.AllocPath`：从对应 `FrameAlloc` 到逃逸点的最短 CFG 证据。
- `Finding.LeakPath`：一条未经过该槽提升的 `FrameAlloc -> 逃逸点` 最短路径。
- `Finding.MissingPromotionPaths`：从捕获点到逃逸点、不含该槽提升的最短路径集合。
- `Capture.Origins`：合流后保留下来的 by-ref 捕获来源。
- `Stats`：访问状态数、转移数、预算和是否耗尽。

## 解释器验证

`ExecutePath` 按操作序列执行一个小型解释器：

1. `FrameAlloc` 用帧模板初始化租户槽字节。
2. `HeapPromote` 把槽值复制进唯一 `HeapCell`。
3. 帧退出时把槽全部填充为攻击租户的字节。
4. 执行异步、全局、返回值和堆中逃逸的闭包。
5. by-ref 捕获若仍指向未提升槽，就记录一次 `Leaked` 读取；提升后的捕获从堆单元读回原租户值。

`VerifyFinding` 会选择 `Finding.LeakPath` 重放，并核对报告中捕获位置确实读到了攻击租户的值。

## 测试

```sh
go test ./...
go test -race ./...
```

覆盖：未提升异步逃逸及解释器跨租户读取、已提升安全、分支只一侧提升、闭包嵌套捕获、by-value 安全、提升后多个闭包共享同一 `HeapCell`、提升改写旧别名/晚提升、捕获已返回帧、`CallNow` 同步安全、四类逃逸点、合流来源集合、预算未知、按 `CallSite+FrameID` 区分递归实例。

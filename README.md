# pkgresolve

一个小型 Go 依赖解析库：在**给定的有限版本清单**（封闭世界）内求解依赖锁定。

## 模型

- `Manifest` 列出每个包的全部可用版本（升序），求解器不会选择清单外的版本。
- 每个版本可声明依赖 `Dep{Name, Min, Max}`，版本区间为**闭区间** `[Min, Max]`。
- `Conflict` 声明一对**不能共存**的具体包版本。
- 每个包在解中**最多一个版本**（可以缺席，除非被请求或被依赖）。

## 求解目标（按优先级）

1. **请求包的最高版本向量**：按 `requests` 给定顺序做字典序最大化
   （`requests[0]` 的版本优先级最高）。
2. **最少传递包数**：在请求向量固定的前提下，最小化锁定包的总数。

## 输出

`Solve` 返回 `Result`：

- 有解：`Result.Locked` 为 `map[包名]版本` 的依赖锁定，`Result.String()`
  输出确定性的锁文件文本（按包名排序的 `name version` 行）。
- 无解：`Result.Reason` 给出原因，例如区间交集为空、版本对冲突、
  依赖的包不在清单中等。

清单本身不合法（区间倒置、版本重复/未排序、引用未知包）时返回 `error`。

## 示例

```go
res, err := resolve.Solve(manifest, []string{"app", "web"})
if err != nil {
    log.Fatal(err) // 清单不合法
}
if !res.OK() {
    log.Fatalf("无解: %s", res.Reason)
}
fmt.Print(res.String()) // 锁文件
```

## 验证

`solve_test.go` 中的 `TestExhaustiveCrossCheck` 对数千个随机生成的小清单，
将求解器结果与**穷举所有赋值**的暴力法逐一比对（可行性、请求版本向量、
包数目标完全一致），并独立校验返回锁定的可行性。

```sh
go test ./...
```

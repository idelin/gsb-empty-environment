# 模块压缩包条目名净化

Go 模块代理收到上传的模块 zip，攻击者用路径逃逸和仅大小写不同的重名条目污染缓存。为Go代理写可导入的条目准入包：每个条目名以 module@version/ 开头，其后不得有空段、. 或 .. 段、反斜杠，不得与其他条目仅大小写不同；vendor 目录和嵌套 go.mod 子树下的文件拒绝，根 go.mod 须恰有一份。

## 导入

```go
import "example.org/netsec/q0841/entryadmit"
```

## 准入规则

`entryadmit.Checker` 逐条检查一个模块 zip 的条目名，任一违规即拒绝整个归档：

- 条目名必须以 `module@version/` 开头（`entryadmit.ErrBadPrefix`）。
- 前缀之后不得出现空段、`.` 段（`ErrInvalidPath`）或 `..` 逃逸段（`ErrTraversal`），任何反斜杠一律拒绝（`ErrInvalidPath`，同时拦截 Windows 风格 `..\..\` 逃逸）。
- 同一父目录下的路径段不得仅大小写不同，精确重名条目同样拒绝（`ErrCaseConflict`），按段作用域判定，避免整名比较漏掉目录名冲突。
- 任意深度的 `vendor` 目录条目拒绝（`ErrVendor`）。
- 嵌套 `go.mod` 本身及其整个子树拒绝（`ErrNestedGoMod`）；即使 `go.mod` 晚于其子树文件出现，也会回溯失败，结论与条目顺序无关。
- 根 `module@version/go.mod` 必须恰有一份，缺失或重复均失败（`ErrRootGoMod`，重复根 go.mod 先命中 `ErrCaseConflict`）。

错误均为 `*entryadmit.Error`（含被拒条目名）并包装对应哨兵错误，可用 `errors.Is` 分类。

## 用法

流式校验（边解压边准入）：

```go
checker, _ := entryadmit.New(modulePath, version)
for _, f := range zip.File {
    if err := checker.Admit(f.Name); err != nil {
        return err // 拒绝整个上传，不要写入缓存
    }
}
if err := checker.Close(); err != nil {
    return err // 缺少根 go.mod 等归档级错误
}
```

一次性校验完整条目列表：

```go
if err := entryadmit.Check(modulePath, version, names); err != nil { ... }
```

测试：`go test ./...`

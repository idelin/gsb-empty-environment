# Tcl 安全解释器访问路径令牌翻译

`netsec` 为 Go 宿主提供安全从属解释器主侧别名所需的路径边界。访问路径列表的下标构成虚拟令牌编号，令牌形如：

```text
$p(:0:)/scripts/init.tcl
$p(:1:)/
```

令牌编号从 0 开始。调用方不能通过令牌直接得到可安全使用的真实路径。

## 使用

```go
translator, err := netsec.New(
    []string{"/srv/tcl/site", "/srv/tcl/lib"},
    []string{"scripts", "modules"}, // 可选：仅允许每个根下的这些子目录
)
if err != nil {
    return err
}
defer translator.Close()

script, err := translator.Source("$p(:0:)/scripts/init.tcl")
matches, err := translator.Glob("$p(:0:)/scripts/*.tcl")
```

`New(roots, subdirs)` 把同一子目录白名单应用于每个根；空白名单表示允许完整根。若每个根需要不同白名单，可使用：

```go
translator, err := netsec.NewWithAccessPaths([]netsec.AccessPath{
    {Root: "/srv/tcl/site", Subdirs: []string{"scripts"}},
    {Root: "/srv/tcl/lib", Subdirs: []string{"modules"}},
})
```

主要接口：

- `Translate`：规范化并解析当前符号链接，返回真实路径用于诊断/兼容；结果不能拿去做安全检查后再打开，否则会重新引入 TOCTOU。
- `Open` / `OpenSource`：通过构造时预打开的目录句柄相对打开，并用 `fstat` 检查最终句柄。`OpenSource` 只接受常规 `.tcl` 文件。
- `Source`：调用 `OpenSource` 后读取内容，供主解释器代为执行。
- `Glob`：在根句柄内展开模式，逐项验证符号链接并反向翻译为令牌。任一项失败时整批返回 `nil` 和错误。
- `Tokenize`：把真实路径反向映射为令牌；嵌套访问路径优先选择最长、最具体的根。

## 安全边界

- 令牌编号越界、格式错误、`..` 上穿根目录均返回错误。
- 真实路径经 `EvalSymlinks` 规范化后必须仍位于对应访问根或允许子目录内。
- 最终打开使用 `os.Root` 的句柄相对解析，目录在翻译后被重命名或替换为外部符号链接也不会逃逸。
- 访问根和允许子目录在构造时打开并持有；允许子目录本身被替换后，打开仍锚定原目录 inode。
- glob 的符号链接目录遍历也会被验证，不依赖 `fs.Glob` 忽略 I/O 错误的默认行为。
- 错误只包含 `$p(:N:)` 等虚拟令牌，不包含真实根前缀；嵌套根也不会因较短的根前缀被剥离而泄漏。

## 验证

`security_test.go` 覆盖伪造/溢出编号、令牌拼接 `..`、文件及目录符号链接逃逸、打开前替换目录、glob 混合结果整批失败、通配符中间符号链接、嵌套路径错误信息、子目录白名单和 `.tcl` 限制。

运行：

```sh
go test ./...
```

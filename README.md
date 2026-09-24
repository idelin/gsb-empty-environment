# Canonical Key

使用 Go 标准库实现的结构化请求规范化键。规范化字节是唯一判定依据，`sha256` 摘要只用于缩短最终键；即使强制摘要碰撞，本地目录也会复核原规范字节并隔离不同输入。

## 规范化规则

- 请求键包含 `tenant`、`parameter_type`、`config_version` 和 `params`。
- 对象按字段的 UTF-8 字节序排序，因此对象字段顺序无关。
- 数组按原始顺序编码，数组位置是语义的一部分。
- JSON 数字以十进制数学值精确规范化，不使用浮点；`1`、`1.00` 和 `1e0` 等价。
- 数字 `1` 与字符串 `"1"` 有不同类型标签，不会碰撞。
- 省略 `params`、显式 `null` 和有值三种状态分别编码。
- `config_version` 是必填字段；省略、显式 `null`、空字符串都会拒绝，避免升级前后错命中。
- Unicode 不做 NFC/NFD 规范化；输入必须是合法 UTF-8，排序使用 Go `sort.Strings` 的 UTF-8 字节序。
- 所有长度和嵌套深度受 `Limits` 预算控制。
- 显式 `Graph` 支持共享节点和循环引用；引用语义使用 `R` 标签和节点表，不与普通字符串或其他类型混码。
- 图节点重命名改变引用身份，不会与原图同键；嵌套图有独立引用作用域。

## 注入与资源

- `Source` 提供数据源；测试中可注入内存 reader、读失败或取消上下文。
- `Clock` 提供目录项时间戳，便于确定性测试。
- `FailureFunc` 可在打开、读取、规范化、目录写入和关闭阶段注入失败。
- `MemoryCatalog` 只使用本地内存，不依赖远程缓存，可通过 `Close` 释放/关闭。
- `Service.Resolve` 在所有返回路径关闭 `io.ReadCloser`，失败注入和取消路径有测试覆盖。

## 成对探测

`GeneratePairs` 生成固定的语义对，覆盖数字/字符串、缺省/null、对象顺序、数组顺序、租户、参数类型和配置版本。`CheckPairs` 会查找：

- 不等价输入产生同键的碰撞反例；
- 等价输入产生不同键的分歧反例。

测试同时用一个故意忽略 `params` 和 `config_version` 的坏键函数，验证探测器确实能找到“数字 1 与字符串 1 同键”的反例。

## 运行

```sh
go test ./...
go vet ./...
go run ./cmd/canonicalkey-example
```

当前环境的 Go 构建缓存目录受沙箱限制，验证时使用了 `/tmp` 缓存：

```sh
GOCACHE=/tmp/canonicalkey-go-cache go test ./...
GOCACHE=/tmp/canonicalkey-go-cache go vet ./...
GOCACHE=/tmp/canonicalkey-go-cache go test -race ./...
GOCACHE=/tmp/canonicalkey-go-cache go run ./cmd/canonicalkey-example
```

## 样例结果

内置样例展示同租户和参数类型下，数字 1、字符串 1，以及升级后的配置版本会得到三个不同键。输出已保存到 `sample-output.txt`，详细测试输出已保存到 `test-output.txt`。

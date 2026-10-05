# strsolver

字符串约束求解器：在有限字符域上为若干字符串变量求值，满足长度上下界、
前缀、后缀、禁用子串与两变量拼接相等约束，返回最优解或不可满足。

## 输入格式（JSON）

```json
{
  "alphabet": "ab",
  "variables": [
    {"name": "X", "minLen": 1, "maxLen": 3, "prefix": "a", "suffix": "b"}
  ],
  "forbidden": ["aa"],
  "equalities": [{"left": ["X", "Y"], "right": ["Z", "W"]}],
  "budget": 100000
}
```

- `alphabet`：有限字符域，字符互不相同；字典序按字符出现顺序（前者更小）。
- `variables`：变量列表，每个变量有长度界 `[minLen, maxLen]` 与可选前缀、后缀。
- `forbidden`：禁用子串列表，作用于所有变量按声明顺序拼接出的完整串，
  因此可跨越变量边界形成。
- `equalities`：拼接相等约束，每侧列出一至两个变量，两侧拼接结果须相等。
- `budget`：搜索状态数预算；`<= 0` 表示不限。

## 目标与语义

- 优化目标：先最小化总长度（各变量长度之和），再取拼接串 `X1..Xn`
  在字母表序下的字典序最小者。
- 输出状态：
  - `SAT`：附带完整赋值 `assignment` 与 `totalLength`；
  - `UNSAT`：搜索空间已在预算内完全枚举，确无可行解；
  - `UNKNOWN`：预算（按搜索状态计）耗尽、搜索未完成——此时绝不报 `UNSAT`。
- 赋值可独立检查：`verify` 子命令用与求解器完全分离的逻辑逐项核对
  （字符域、长度界、前缀、后缀、拼接串禁用子串、拼接相等）。

## 用法

```sh
go build -o strsolver .
./strsolver solve problem.json            # 输出解的 JSON
./strsolver verify problem.json sol.json  # 独立核对赋值，OK/FAIL
```

`sol.json` 可以是 `solve` 的完整输出，也可以只是 `{"X": "a", ...}` 的裸赋值映射。

## 测试

```sh
go test ./...
```

- 单元测试：跨变量边界禁用子串、拼接相等、前后缀重叠、预算语义、验证器防篡改。
- 穷举对照：短域实例（含全部长度-2 禁用子集、400 个随机实例）与暴力枚举
  逐一比对最优性（总长度、字典序），SAT 赋值再经独立验证器核对。

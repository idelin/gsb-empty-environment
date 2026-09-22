# 本地能力提供者选择器

Python 3.11 标准库实现，不联网、不访问包仓库。输入是一个本地 JSON 文件，描述实现包、它们提供的能力、依赖、互斥项、构建目标和版本约束；输出每个能力最终由哪个实现包提供。

## 规则

- 能力可由多个包提供，例如能力 `X` 可由 `A` 或 `B` 提供。
- 同一能力在最终结果中只有一个提供者；多个消费者会复用同一个提供者。
- 候选包发生互斥、目标不匹配、版本不满足或后续依赖无解时，求解器回退并尝试下一个实现。
- 替代组使用 `any_of`，按稳定决胜规则排序；一个选项失败后继续尝试后续选项。替代链通过包的 `requires` 延续，不支持在 JSON 中直接嵌套 `any_of`。
- 替代链成环时检测为无解并终止，不会无限递归。
- 版本遵循 `major.minor.patch`，支持 SemVer 预发布，例如 `1.2.3-rc.1`；普通稳定版高于同版本预发布版。

稳定决胜顺序如下：

1. 满足全部版本约束。
2. 满足构建目标。
3. 未显式请求或允许时，排除预发布能力和预发布包。
4. 能力版本更高者优先。
5. 包版本更高者优先。
6. 包名字典序更小者优先。
7. 能力声明字典序更小者优先。

## JSON 输入

见 `examples/sample_manifest.json`。主要字段：

- `packages[].name` / `version`：实现包名称和版本。
- `packages[].provides`：字符串或对象；对象可声明能力自己的 `version` 与 `targets`。
- `packages[].requires`：普通需求或 `{ "any_of": [...] }` 替代组。
- `packages[].conflicts`：可按包名或能力声明互斥，并可加版本约束。
- `packages[].targets`：包支持的目标。
- `builds[].target`：当前构建的唯一目标。
- `builds[].requirements`：构建入口需求。
- `builds[].allow_prereleases`：`true`、能力名或能力名数组。

支持的版本运算符：`*`、`==`/裸版本、`!=`、`>=`、`<=`、`>`、`<`、`^`、`~`。

## 运行

```bash
python3.11 -m package_selector examples/sample_manifest.json --build linux-app
```

该样例中 `impl-a` 提供能力 `X@3.0.0`，但与入口要求的 `block-a` 冲突；求解器自动回退到 `impl-b@1.5.0`。

关键输出：

```json
{
  "capability_to_implementation": {
    "X": "impl-b@1.5.0",
    "a-blocker": "block-a@1.0.0",
    "json": "json-parser@4.1.0",
    "policy": "shared-policy@1.2.0"
  }
}
```

允许 `X` 使用预发布并选择更高能力版本：

```bash
python3.11 -m package_selector examples/sample_manifest.json --build prerelease-linux
```

此时 `X` 由 `impl-a@2.0.0` 提供。

## 测试

```bash
python3.11 -m unittest discover -s tests -v
```

本机结果：

```text
Ran 15 tests in 0.004s

OK
```

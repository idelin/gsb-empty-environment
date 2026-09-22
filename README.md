# 本地 JSON 依赖解析器

纯标准库实现（Python 3.11），不联网查包。从本地 JSON 读取包索引与根依赖，
为根依赖选择一组一致版本：按包名字典序处理，每个包在满足全部约束（含传递约束）
的前提下选尽可能新的版本。支持区间、排除点（`!=`）、精确版本与通配；预发布版本
仅在显式允许时进入候选；版本兼容的循环依赖可解；冲突时输出可复查的约束集合。

## 输入格式

```json
{
  "packages": {
    "包名": {
      "1.0.0": {"依赖包名": ">=1.0.0,<2.0.0"}
    }
  },
  "roots": {"包名": "约束"},
  "allow_prerelease": ["包名"]
}
```

- 版本：`major.minor.patch`，可带预发布后缀（如 `2.0.0-rc.1`），按 semver 规则排序
  （`2.0.0-rc.1 < 2.0.0`）。
- 约束：逗号分隔的子句取交集，支持 `>=` `<=` `>` `<` `==` `!=`、裸版本（等价 `==`）、
  `*` 或空串（任意）。
- `allow_prerelease`：`true`（全部允许）、包名列表（`"*"` 表示全部），可省略。
  此外，约束中显式引用预发布版本（如 `>=2.0.0-rc.1`）也会为该包开启预发布候选。

## 运行

```sh
python3.11 resolver.py examples/app.json
```

输出（`util` 的 `2.1.0-beta` 是预发布，未显式允许故不入选）：

```json
{
  "status": "resolved",
  "versions": {
    "app": "1.0.0",
    "lib": "1.9.0",
    "util": "2.0.0"
  }
}
```

冲突示例（`a` 要求 `c<2.0.0`，`b` 要求 `c>=2.0.0`）：

```sh
python3.11 resolver.py examples/conflict.json   # 退出码 1
```

```json
{
  "status": "conflict",
  "detail": {
    "package": "c",
    "constraints": [
      {"constraint": "<2.0.0", "required_by": "a@1.0.0"},
      {"constraint": ">=2.0.0", "required_by": "b@1.0.0"}
    ],
    "available_versions": ["2.1.0", "2.0.0", "1.5.0", "1.0.0"]
  }
}
```

循环依赖但版本兼容（`a <-> b`）可正常求解：

```sh
python3.11 resolver.py examples/cycle.json
# {"status": "resolved", "versions": {"a": "1.0.0", "b": "1.1.0"}}
```

## 测试

```sh
python3.11 -m unittest -v
```

结果：`Ran 25 tests ... OK`（覆盖版本解析/排序、区间与排除点、传递约束、
字典序回溯、冲突约束集合、预发布三种开启方式、循环依赖、未知依赖、CLI）。

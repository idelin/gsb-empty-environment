# depres — 本地 JSON 依赖解析器

纯标准库（Python 3.11+），不联网。为根依赖选择一组一致版本：支持区间、
排除点（`!=`）与传递约束；按包名字典序依次解析，每个包取满足全部约束的
最新版本，必要时回溯降级先选的包；冲突时输出可复查的约束集合（含来源）。

## 输入格式

```json
{
  "requirements": {"app": ">=1.0.0", "cli": "*"},
  "packages": {
    "app": {
      "1.2.0": {"dependencies": {"core": ">=1.4.0, <2.0.0", "util": "*"}}
    }
  }
}
```

- 版本：`major.minor.patch`，可带预发布后缀（如 `2.0.0-rc.1`），按 semver 排序。
- 约束：逗号/空格分隔的比较子句，支持 `>= <= > < == !=`；裸版本号等价于
  `==`；`*` 或空串表示任意版本。
- 预发布版本默认不进入候选，仅当该包的某条约束显式提到预发布版本
  （如 `>=2.0.0-rc.1`）时才允许。

## 运行

```console
$ python3 -m depres examples/success.json
app 1.2.0
cli 0.9.0
core 1.6.0
util 0.4.0

$ python3 -m depres examples/conflict.json
conflict: no version of 'C' satisfies all of these constraints:
  <2.0.0  (required by A@1.0.0)
  >=2.0.0  (required by B@1.0.0)
# 退出码 1

$ python3 -m depres examples/prerelease.json   # 显式允许预发布
lib 2.0.0-rc.2

$ python3 -m depres examples/cyclic.json       # 版本兼容的循环依赖可解
A 1.0.0
B 1.5.0
```

## 测试

```console
$ python3 -m unittest discover -s tests
Ran 26 tests in 0.003s

OK
```

## 结构

- `depres/version.py` — 版本解析与 semver 排序（预发布 < 正式版）
- `depres/constraints.py` — 约束解析与匹配（区间、排除点、预发布门槛）
- `depres/registry.py` — 本地 JSON 注册表
- `depres/resolver.py` — 回溯求解器与 `ResolutionConflict` 冲突报告
- `depres/__main__.py` — CLI 入口
- `tests/` — unittest 测试（26 个）
- `examples/` — 成功 / 冲突 / 预发布 / 循环依赖样例

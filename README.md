# lockmerge

三方锁文件（lock file）合并工具。Python 3.11，仅使用标准库；包与构建输入全部来自
本地 JSON 文件，不联网查包。版本号为 `major.minor.patch`，可带预发布后缀
（如 `2.0.0-rc.1`），按 semver 规则排序。

## 输入格式

注册表（registry，本地可用版本及其依赖约束）：

```json
{"packages": {"web": {"1.1.0": {"dependencies": {"utils": ">=1.2.0,<2.0.0"}}}}}
```

锁文件（base / ours / theirs，锁定精确版本并记录依赖边）：

```json
{"packages": {"web": {"version": "1.1.0", "dependencies": {"utils": ">=1.2.0,<2.0.0"}}}}
```

约束为逗号分隔的比较子句：`>=`、`<=`、`>`、`<`、`==`、`!=`，`"*"` 或空串匹配全部。

## 合并规则

- 仅一方变更的包：自动合并，来源记为 `ours` / `theirs`。
- 双方相同变更（含相同删除）：只保留一次，来源记为 `both`。
- 双方对同一包的不同改动（含一方删除、一方修改）：不可自动处理，
  记入 `unresolved`（`pin-conflict`），该包保留 base 版本。
- 双方升级不同根包导致共享依赖约束冲突时，不直接拼接，而是重新验证闭包：
  汇总合并结果中对该共享包的全部约束，从本地注册表选取满足所有约束的
  最高版本（来源记为 `re-resolved`）；重解析引入的新依赖会自动补齐；
  本地无任何版本可满足时记入 `unresolved`（`shared-dependency-conflict`）。
- 被删除的包仍被其他包引用时拒绝合并，记入 `unresolved`（`dangling-reference`）。

## 输出

报告包含：`status`（`ok` / `conflict`）、`provenance`（每个包的来源）、
`changes`（自动应用的变更及来源）、`unresolved`（不可自动处理项）、
`merged`（合并后的锁文件，冲突时为尽力结果）。合并成功退出码为 0，否则为 1。

## 运行

```sh
python3.11 -m lockmerge --base examples/base.json --ours examples/ours.json \
    --theirs examples/theirs.json --registry examples/registry.json \
    --out examples/merged.json
```

- 可自动合并样例输出见 `examples/run_sample.txt`。
- 共享依赖不可调和冲突样例见 `examples/conflict/run_sample.txt`。

## 测试

```sh
python3.11 -m unittest discover -s tests -v
```

# versionset — 符号化版本集合与本地依赖求解

纯 Python 3.11 标准库实现，无第三方依赖、不联网。

## 功能

- **版本域**：`major.minor.patch`，可带预发布标签（如 `1.2.3b1`，通道 `b` 编号 `1`）。
  稳定版排在同三元组的全部预发布之后；域无限，绝不枚举。
- **版本集合 `VersionSet`**：区间的并，每个分量附带
  - 预发布**通道**集合（默认仅 `stable`，预发布只在通道显式允许时混入）；
  - **目标条件**集合（如 `linux`，按构建目标判定成员资格）；
  - 区间支持无界端点、开/闭边界、排除点（点集求差）。
- **运算**：并 / 交 / 差（对合取式按维度精确拆分，符号化）、包含判定、
  **最小反例**（对称差中的最小版本，由后继函数与通道向上取整直接算出）。
- **规范形式**：区间排序合并 + 同标签分量合并 + 吸收分量剔除；
  语义等价由对称差判定。提供规范 JSON 序列化（`canonical()`）与
  SHA-256 哈希（`digest()`）。
- **求解器 `Resolver`**：包与构建输入全部来自本地 JSON（`fixtures/`），
  回溯 + MRV，支持循环依赖、锁版本固定；每条约束记录来源，
  `explain_lock_change` 给出锁变更时造成选择改变的约束；
  不可满足时抛出带结构化冲突与人类可读解释的 `Unsatisfiable`。

## 运行

```sh
python3.11 -m unittest discover -s tests -v
```

## 布局

- `versionset/version.py` — 版本、全序、后继函数
- `versionset/labels.py` — 通道/目标的有限标签集（含全集减）
- `versionset/intervals.py` — 区间运算（开闭/无界端点）
- `versionset/sets.py` — `VersionSet` 正规形、布尔运算、最小反例、序列化
- `versionset/repo.py` — 本地 JSON 包仓库与构建输入
- `versionset/resolver.py` — 传递依赖求解、约束溯源、锁变更解释
- `tests/` — unittest 测试（含穷举交叉验证）
- `fixtures/` — 包仓库与构建输入 JSON
- `RESULTS.md` — 实际运行记录

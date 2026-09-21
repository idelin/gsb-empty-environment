# 声明式“能力 → 实现”解析器

多个包可以声明提供同一能力（capability）。解析器读取**本地**包目录和构建输入
（均为 JSON，全程不联网），综合**版本约束、互斥（conflicts）和目标平台约束**，
为每个被请求的能力选出**唯一**实现，并输出能力到实现的映射，以及保证跨进程可复现的
稳定决胜规则。

- 运行环境：Python 3.11+（仅标准库）
- 测试框架：`unittest`
- 文件：`resolve.py`（实现）、`test_resolve.py`（26 个测试）、`examples/`（样例）

## 输入格式

包目录 `packages.json`：

```json
{
  "packages": [
    {
      "name": "impl-a",
      "version": "1.2.0",
      "priority": 10,
      "provides": [
        {"capability": "X", "version": "1.2.0", "priority": 10}
      ],
      "requires": [{"capability": "net", "version": ">=2.0.0"}],
      "conflicts": ["legacy-core"],
      "alternatives": ["impl-b"],
      "os": ["linux"],
      "arch": ["x86_64"]
    }
  ]
}
```

- `provides` 中的 `version` 省略时默认等于包版本；裸字符串 `"X"` 等价于
  `{"capability": "X", "version": <包版本>}`。
- 版本为 `major.minor.patch`，可带 SemVer 预发布后缀（如 `1.3.0-beta.1`）。
- 版本约束支持 `>= <= > < == != =`，以及 `^`（兼容上限，npm 语义）和
  `~1.2.x`（次版本固定）；多个约束取交集。
- `conflicts` 为包名级互斥；`alternatives` 声明显式替代偏好。

构建输入 `build.json`：

```json
{
  "target": {"os": "linux", "arch": "x86_64"},
  "allow_prereleases": false,
  "requires": [
    "X",
    {"capability": "net", "version": ">=2.0.0"},
    {"capability": "Z", "optional": true}
  ]
}
```

## 选择与决胜规则（稳定、可复现）

1. **单提供者**：同一能力最终只能由一个包提供；一个包可同时提供多个能力。
2. **候选过滤**：保留满足目标 `os/arch` 的包；`allow_prereleases=false` 时剔除预发布。
3. 同一能力内依次比较：`provides.priority` 降序 → 能力版本 SemVer 降序 →
   包级 `priority` 降序 → 包版本降序 → `name@version` 字典序。
4. `alternatives` 表示显式替代偏好：没有“被指向”入边的直接提供者优先；
   **替代链成环时**取剩余集合中排序最高的成员破环，并用已访问集合去重，
   因此绝不会无限递归。
5. 顶级要求按 `(priority 降序, capability 升序)` 处理；选中包的 `requires`
   作为传递要求一并求解。
6. 首选实现若违反版本约束、单提供者或 `conflicts` 互斥，则**回溯**候选链中的
   下一个实现（如 A 冲突时尝试 B）。
7. 可选要求（`optional`）在无可行实现时安全跳过；无任何可行解时返回退出码 2
   并给出原因。

## 运行样例

```bash
python3 resolve.py --packages examples/packages.json --build examples/build.json
```

样例目录中 `legacy-core`（提供能力 `Y`）与 `impl-a` 互斥，而 `impl-a` 因更高的
`provides.priority` 是能力 `X` 的首选。求解时记录到：

```text
Y     -> legacy-core@1.0.0
X     -> impl-b@2.0.0        # impl-a 与 legacy-core 互斥，回溯到备选 B
net   -> netstack@2.1.0      # impl-a 的传递要求 >=2.0.0
json  -> json-rapid@1.4.2    # 同版本同名能力按包名字典序决胜
K     -> kv-rocks@1.0.0      # 纯 alternatives 环，取排序最高者破环
```

决策轨迹中与 `X` 相关的条目（完整 JSON 见 `examples/result.json`）：

```text
{'capability': 'X', 'action': 'reject',   'package': 'impl-a@1.2.0',
 'reason': '与已选包 legacy-core@1.0.0 互斥'}
{'capability': 'X', 'action': 'select',   'package': 'impl-b@2.0.0', ...}
```

允许预发布、并把目标换成 macOS/arm64：

```bash
python3 resolve.py --packages examples/packages.json --build examples/build_prerelease.json
# X -> impl-a@1.3.0-beta.1
```

不可满足时（Windows 上没有任何 `Y` 实现）：

```text
错误: 无法满足能力要求: Y；被拒能力: Y      # 退出码 2
```

## 测试

```bash
python3 -m unittest test_resolve -v
```

覆盖：SemVer 与预发布排序、比较/`^`/`~` 约束、版本决胜、priority 决胜、
同版本包名决胜、A 冲突回溯到 B、无备选时失败、单提供者、目标平台过滤、
预发布开关、替代链成环终止、传递依赖与深度回溯、可选要求跳过、跨运行确定性，
以及输入校验（重复包、未知 alternatives、未知能力、非法约束）。

最近一次结果（Python 3.13，标准库 `unittest`）：

```text
..........................
----------------------------------------------------------------------
Ran 26 tests in 0.002s

OK
```

# buildset — 条件依赖的最小构建集合解析器

纯 Python 3.11 标准库实现（测试用 `unittest`）。包与构建输入全部来自本地 JSON
文件，不联网查包。版本格式严格为 `major.minor.patch`，可带预发布标签
（如 `2.2.0-beta.1`），按 semver 优先级参与比较。

## 功能

- 依赖边携带目标 OS、架构、阶段（stage）条件，只对条件为真的边求传递闭包，
  得到最小实际构建集合。
- 每条依赖边输出条件求值证据（`evidence`）：逐字段记录要求值、实际值、是否通过。
- 交叉编译：`kind: "tool"` 的包（生成器等）按宿主运行，缓存键为宿主 OS/架构；
  `kind: "library"` 的包按目标编译，缓存键为目标 OS/架构。两个目标共享同一份
  宿主工具缓存。
- 宿主工具与目标产物分开缓存：`<cache>/host/<os>-<arch>/...` 与
  `<cache>/target/<os>-<arch>/...`。
- 假条件依赖不参与版本冲突：只有条件为真的边才贡献版本约束。

## 输入格式

`data/packages.json` — 本地包注册表：

```json
{
  "packages": [
    {
      "name": "libcore",
      "version": "1.5.0",
      "kind": "library",
      "dependencies": [
        {"name": "libnet", "constraint": ">=1.0.0",
         "when": {"os": ["linux", "darwin"]}}
      ]
    }
  ]
}
```

- `kind`：`library`（按目标编译）或 `tool`（生成器，按宿主运行）。
- `constraint`：逗号或空格分隔的版本子句，支持 `>= <= == != > <`，
  裸版本号表示精确匹配，空串或 `*` 表示任意。
- `when`：可选，字段为 `os` / `arch` / `stage`，值为可接受列表（或单字符串）。

`data/build_request.json` — 构建请求：

```json
{
  "host": {"os": "linux", "arch": "x86_64"},
  "target": {"os": "darwin", "arch": "arm64"},
  "stage": "build",
  "roots": [{"name": "app", "constraint": ">=2.0.0"}]
}
```

## 运行

```console
$ python3 -m buildset --packages data/packages.json \
      --request data/build_request.json --cache-dir /tmp/buildset-cache
```

输出 JSON 报告（节选）：构建集合按依赖优先排序，每个节点带缓存分区与缓存键；
`evidence` 给出每条边的求值证据；`pruned` 列出被剪掉的假条件边。

```json
{
  "build_set": [
    {"name": "libcrypto", "version": "2.2.0-beta.1", "kind": "library",
     "partition": "target", "cache_key": ["libcrypto", "2.2.0-beta.1", "darwin", "arm64"]},
    {"name": "libnet",    "version": "1.1.0", "kind": "library",
     "partition": "target", "cache_key": ["libnet", "1.1.0", "darwin", "arm64"]},
    {"name": "libcore",   "version": "1.5.0", "kind": "library",
     "partition": "target", "cache_key": ["libcore", "1.5.0", "darwin", "arm64"]},
    {"name": "templates", "version": "1.4.0", "kind": "tool",
     "partition": "host", "cache_key": ["templates", "1.4.0", "linux", "x86_64"]},
    {"name": "codegen",   "version": "3.1.0", "kind": "tool",
     "partition": "host", "cache_key": ["codegen", "3.1.0", "linux", "x86_64"]},
    {"name": "app",       "version": "2.0.0", "kind": "library",
     "partition": "target", "cache_key": ["app", "2.0.0", "darwin", "arm64"]}
  ],
  "evidence": [
    {"package": "libcore@1.5.0", "dependency": "libwin", "constraint": ">=2.0.0",
     "condition": {"os": ["windows"]},
     "checks": [{"field": "os", "required": ["windows"], "actual": "darwin",
                 "passed": false}],
     "taken": false}
  ],
  "pruned": ["libcore@1.5.0 -> libwin", "libcore@1.5.0 -> libdbg"]
}
```

注：`libcrypto` 的约束 `>=2.1.0` 选中了预发布版 `2.2.0-beta.1`——预发布版本
按 semver 优先级参与匹配（`2.2.0-beta.1 > 2.1.0`）。

## 缓存行为（实测）

同一缓存根下先构建 `darwin/arm64`，再构建 `windows/x86_64`：

```console
# 第二次 darwin/arm64：全部命中
"hits": [".../target/darwin-arm64/...", ".../host/linux-x86_64/..."], "stored": []

# windows/x86_64：宿主工具命中（两目标共享），目标产物另存
hits:
  /tmp/buildset-cache/host/linux-x86_64/templates/1.4.0
  /tmp/buildset-cache/host/linux-x86_64/codegen/3.1.0
stored:
  /tmp/buildset-cache/target/windows-x86_64/libcrypto/2.2.0-beta.1
  /tmp/buildset-cache/target/windows-x86_64/libwin/2.3.0
  ...
```

缓存目录树：

```
/tmp/buildset-cache
├── host/linux-x86_64/{codegen/3.1.0, templates/1.4.0}
└── target/{darwin-arm64, windows-x86_64}/<name>/<version>
```

## 冲突

条件为真的边对同一包提出不兼容约束时，命令以退出码 1 结束并输出冲突详情
（包名、各约束及来源）。假条件边不贡献约束，因此不会引发冲突。

## 测试

```console
$ python3 -m unittest discover -s tests -v
...
Ran 37 tests in 0.022s

OK
```

覆盖：版本解析与预发布排序、约束匹配、条件求值证据、最小集合剪枝、
阶段条件、最高满足版本选择、拓扑构建顺序、真/假条件边的冲突行为、
交叉编译分区（工具→宿主、库→目标）、两目标共享宿主工具缓存键、
缓存命中/未命中与分区隔离、CLI 报告与冲突退出码。

## 代码结构

- `buildset/version.py` — 语义化版本（含预发布）与版本约束。
- `buildset/conditions.py` — 依赖边条件与求值证据。
- `buildset/registry.py` — 本地 JSON 包注册表（无网络）。
- `buildset/resolver.py` — 最小构建集合求解、冲突检测、宿主/目标分区。
- `buildset/cache.py` — 宿主/目标分离的构建缓存。
- `buildset/cli.py` — 命令行入口（`python3 -m buildset`）。

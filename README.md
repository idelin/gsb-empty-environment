# GSB 删节规划包（C++20）

面向带共享内存页的故障转储采集宿主：同一底层字节可能同时出现在日志片段、私钥片段和它们的别名中。本包把「底层字节身份」和「视图位置」分开建模，生成可导入、可验证、可回读来源证据的删节规划。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j
ctest --test-dir build --output-on-failure
```

目标是 C++20，无第三方依赖。`gsb_plan` 是 object library；`gsb-plan` 是命令行宿主；`gsb_plan_tests` 包含逐字节展开和穷举编辑项子集的独立对照测试。

## 语义

- **底层身份**：`(page_id, byte_offset)`。页编号和偏移是唯一身份。
- **视图**：`page`（页切片）、`concat`（按字节拼接已有视图）、`slice`（对已有视图切片）、`reverse`（逆序）。引用形成无环图；重复引用允许；长度使用 64 位整数并限制在 `2^48` 以内。
- **保密**：顶层导出视图的 `secret` 区间把区间内位置的底层字节身份标记为秘密。秘密是**底层字节身份**的属性；只要任一视图声明了某字节秘密，它在所有导出视图中的所有别名都必须被遮蔽。
- **必留**：每个导出视图的 `keep` 区间按该视图的输出位置定义。区间内位置必须保持原值，不能被任何选中的编辑项覆盖。
- **编辑项**：最多 22 项。每项是某个视图定义内的一个区间，选中一次后覆盖该视图定义的所有展开出现位置；父视图的编辑项独立于子视图。若某项在任何导出视图中覆盖了必留位置，该项不可选。
- **目标**：在合法子集中，使所有秘密字节出现位置被覆盖、所有必留位置未被覆盖，最小化总代价。同价时，按选中编号升序列表做字典序比较（前缀更短者更小）。
- **状态**：`optimal`、`unsat`、`undetermined`。搜索节点预算耗尽时返回 `undetermined`，不会误报无解。`unsat` 附带冲突约束。

## 输入格式

```json
{
  "format": "gsb.redaction.input/v1",
  "pages": [
    {"id": "p", "size": 4, "data_hex": "41424344"}
  ],
  "views": [
    {"id": "a", "op": "page", "page": "p", "start": 0, "length": 4},
    {"id": "dup", "op": "concat", "parts": ["a", "a"]},
    {"id": "rev", "op": "reverse", "source": "dup"},
    {"id": "tail", "op": "slice", "source": "rev", "start": 0, "length": 3}
  ],
  "exports": [
    {
      "id": "e0",
      "view": "rev",
      "keep": [{"start": 1, "length": 1}],
      "secret": [{"start": 0, "length": 1}]
    },
    {
      "id": "e1",
      "view": "tail",
      "keep": [],
      "secret": []
    }
  ],
  "edits": [
    {"id": 0, "view": "a", "start": 3, "length": 1, "cost": 3},
    {"id": 1, "view": "rev", "start": 0, "length": 1, "cost": 1},
    {"id": 2, "view": "rev", "start": 4, "length": 1, "cost": 1}
  ],
  "solver": {"node_budget": 2000000}
}
```

字段说明：

- `pages[].size` 可单独给出；也可给 `data_hex` 或 `data_base64`，数据长度必须与 `size`（若给出）一致。规划本身只依赖身份和长度，不依赖页内容。
- 视图可前向引用，加载时做拓扑排序并检测环。
- `op=page` 使用 `page/start/length`；缺省 `start=0`，缺省 `length` 表示到页尾。
- `op=slice` 使用 `source/start/length`；缺省语义同上。
- `op=reverse` 使用 `source`，长度为源视图长度。
- `op=concat` 使用 `parts`，允许重复引用和空视图。
- 区间均为半开区间 `[start, start+length)`，也接受 `start/end`；全包用 `length` 输出。
- `exports` 可省略；此时视图对象上的 `export: true` 或 `keep`/`secret` 会生成导出视图，导出 id 等于视图 id。
- `edits`（别名 `edit_items`）最多 22 项；`id` 可省略，省略时分配未占用的最小非负编号，最终按编号排序。

## 输出规划

```json
{
  "format": "gsb.redaction.plan/v1",
  "input_sha256": "...",
  "digest_alg": "sha256",
  "status": "optimal",
  "selected": [1, 2],
  "total_cost": 2,
  "conflicts": [],
  "solver": {"nodes_explored": 8, "node_budget": 2000000},
  "policy": {
    "secrecy_domain": "base-byte-identity",
    "masking_domain": "view-definition-interval",
    "budget_exhaustion": "undetermined",
    "tie_break": "lexicographic-sorted-id-list"
  }
}
```

规划只包含编号、代价、摘要和冲突元数据，不包含页字节值。`unsat` 时 `conflicts` 会给出冲突类型，例如 `secret_keep_overlap`（同一底层字节同时被要求保留和遮蔽）、`item_masks_keep`、`uncoverable_secret`。

## 命令行

```sh
# 求解并写出规划
./build/gsb-plan solve --input examples/shared-pages.json \
  --out examples/shared-pages.plan.json

# 校验规划结构、摘要、覆盖/必留，并复核最优性
./build/gsb-plan validate --input examples/shared-pages.json \
  --plan examples/shared-pages.plan.json

# 回读短输出范围的掩码和来源证明（不输出原值）
./build/gsb-plan inspect --input examples/shared-pages.json \
  --plan examples/shared-pages.plan.json \
  --export e0 --start 0 --length 8
```

`validate` 退出码：`0` 有效，`1` 无效。`inspect` 默认最大范围 4096 字节，可用 `--max-inspect` 调整。证据中每个字节只包含：

- `masked` 与 `mask_items`（哪些编辑项覆盖它）
- `secret` 标记
- `base`（页 id 与偏移）
- `path`（从导出视图到页切片的视图/局部偏移链）

证据明确带有 `"contains_plaintext": false` 和 `"values_omitted": true`，不会复制任何页字节值；如果发现未遮蔽的秘密，接口直接失败而不是输出证据。

`validate --no-optimality` 只做结构、摘要和覆盖校验。默认会重新求解并检查最小代价与字典序最小选择；若复核搜索预算耗尽，则给出警告而不是伪造结论。

## 求解方法

求解器不展开长度可达 `2^48` 的视图：

1. 从所有导出的 `secret` 区间收集底层页区间，得到全局 `SecretSet`。
2. 检测必留/保密底层冲突（无条件的 `unsat`）。
3. 对每个编辑项做符号化覆盖查询，剔除会覆盖必留位置的项。
4. 以「未被覆盖的秘密出现位置」为见证做分支：任何可行补全必须选择至少一个覆盖该见证的剩余编辑项。状态用已选位图记忆化，得到精确最小代价。
5. 在最小代价层，按编号升序反复选择「仍能完成最优解」的最小下一个编号；若当前前缀已经覆盖全部秘密则停止，从而得到字典序最小的升序编号列表。

范围查询、秘密身份收集、必留冲突和覆盖查询都在视图 DAG 上递归，按 `(view, start, length)` 记忆化并利用可达编辑项位图剪枝。搜索节点数受 `node_budget` 限制；超过预算返回 `undetermined`。

## 测试覆盖

`tests/test_main.cpp` 包含：

- 逐字节展开 `page/concat/slice/reverse` 的字符串对照。
- 独立穷举全部编辑项子集的暴力求解器，与符号求解器比较代价和字典序选择；含 1000 个随机小图。
- 重复子树末尾一字节：子视图编辑项影响两个副本，父视图编辑项只影响自身位置。
- 反向视图叠加切片：覆盖经 `reverse` 与 `slice` 正确映射。
- 同一底层字节进入必留与保密要求：返回 `secret_keep_overlap`。
- 覆盖组合不等于顶层区间并集：两个共享子视图编辑项的并集才能覆盖秘密别名。
- 跨导出别名、必留项禁用、无解、预算耗尽、SHA-256 向量、证据无明文、篡改规划拒绝。
- `2^48` 长度重复子树：不展开、节点数受控，父视图整段编辑项和子视图单字节编辑项均可比较。

## 目录

- `include/gsb/`：JSON、模型、求解器、证据接口。
- `src/`：实现与 CLI。
- `tests/`：独立暴力对照与边界测试。
- `examples/`：可直接运行的共享页示例。

# whitelist — 反序列化白名单危险对象检查器

给定类型图与反序列化白名单，判断攻击者能否构造一个**合法**（通过白名单）
却**危险**的对象图：同一个实际对象同时从根对象的两个指定字段可达
（共享引用 / 菱形，路径中允许环）。若存在，输出节点数最少的对象图，
并以引用编号序列化（支持环与共享引用）；另提供独立验证模式。

## 构建与测试

```sh
go build -buildvcs=false -o bin/whitelist .
go test ./...
```

## 输入规格（spec.json）

```json
{
  "rootTypes": ["Session"],            // 白名单允许的根类型
  "dangerFields": ["primary", "backup"], // 指定的两个危险字段（根对象的字段）
  "maxObjects": 8,                     // 对象数上限，1..8，默认 8
  "triggeredOnly": true,               // 可选：共享对象必须带触发方法才算危险
  "types": [                           // 类型图
    {"name": "Session", "fields": [
      {"name": "primary", "type": "Handler"},
      {"name": "backup",  "type": "Handler"}]},
    {"name": "Handler", "fields": [{"name": "next", "type": "Handler"}]},
    {"name": "EvilHandler", "triggers": ["readObject"],
     "fields": [{"name": "next", "type": "Handler"}]}
  ],
  "subtypes": {"Handler": ["Handler", "EvilHandler"]}  // 静态类型 -> 可替代子类型
}
```

- `triggers`：类型在反序列化时会触发的方法（如 `readObject`）。
- `subtypes` 缺省的静态类型只允许其自身。
- `triggeredOnly` 为 `true` 时，危险条件收紧为“共享对象的类型至少有一个
  触发方法”；为 `false`（默认）时，纯粹的别名（同一对象从两个字段可达）
  即为危险。

## 危险条件

存在某个实际对象 X，使得 X 从根对象的 `dangerFields[0]` 出发可达，
且同时从 `dangerFields[1]` 出发可达（可达性沿字段引用传播，允许经过环；
`triggeredOnly` 时 X 还需带触发方法）。

## 用法

```sh
whitelist solve  <spec.json>               # 求节点数最少的危险对象图
whitelist verify <spec.json> <graph.json>  # 独立验证序列化结果
whitelist example                          # 打印示例规格
```

### solve

按对象数递增做 0-1 BFS（赋 null / 引用已有对象不增加节点，新建对象 +1），
首次命中的危险图即节点数最少。字段按声明顺序确定性填充，引用已有对象
的选项使搜索天然支持共享引用与环（包括自环）。输出示例：

```json
{
  "dangerous": true,
  "minObjects": 2,
  "shared": [{"id": 1, "type": "EvilHandler", "triggers": ["readObject"]}],
  "objects": [
    {"id": 0, "type": "Session", "fields": {"primary": 1, "backup": 1}},
    {"id": 1, "type": "EvilHandler", "fields": {"next": null}}
  ],
  "render": ["#0: Session{primary=#1, backup=#1}", "#1: EvilHandler{next=null}"]
}
```

字段值是目标对象的引用编号（`id`）或 `null`；共享引用与环通过重复使用
编号表达。若在上限内不存在危险图，输出 `"dangerous": false` 及说明，
表示白名单在该上界内有效。

### verify

独立校验序列化结果，逐项输出 PASS/FAIL，任一失败则以非零码退出：

- 图非空、对象数不超过 `maxObjects`、id 为 `0..n-1` 顺序编号；
- 类型均已知，字段与声明完全一致（不多不少）；
- 白名单合法性：每个字段引用的实际类型 ∈ 静态类型的可替代子类型；
- 根类型 ∈ `rootTypes`，所有对象从根可达；
- 危险条件成立（在序列化形式上独立重算可达性）；
- 最小性：重新搜索并比对节点数。

## 示例

```sh
./bin/whitelist solve examples/diamond.json   # 2 节点：根 + 共享 EvilHandler
./bin/whitelist solve examples/chain.json     # 4 节点：两侧子图在 Leaf 汇合
./bin/whitelist solve examples/cycle.json     # 1 节点：自环 p=#0, q=#0
./bin/whitelist solve examples/safe.json      # dangerous=false，白名单有效
./bin/whitelist solve examples/diamond.json > /tmp/g.json
./bin/whitelist verify examples/diamond.json /tmp/g.json
```

## 限制

对象数上限固定为 8（搜索空间随类型数、字段数、对象数指数增长；
内置 200 万状态的安全阀，超出即报错退出）。

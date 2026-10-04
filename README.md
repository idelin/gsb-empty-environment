# archive-precheck

归档导入前的命名冲突预检工具。比较**不**基于完整路径字符串，而是把每条路径
切分成段、用调用方显式提供的折叠表逐段规范化后插入前缀树（trie），因此
父目录前缀上的冲突（如 `A/b.txt` 与 `a/c.txt`）同样会被捕获。

## 用法

```sh
go build -o precheck .
precheck -fold testdata/fold.json -list testdata/entries.txt
precheck -fold testdata/fold.json -scan /path/to/tree
```

退出码：发现冲突或违规为 `1`，干净为 `0`，用法错误为 `2`。

## 折叠表（显式输入）

```json
{
  "mappings": {"A": "a", "ß": "ss"},
  "reserved_names": ["CON", "PRN", "AUX", "NUL", "COM1", "LPT1"],
  "trim_trailing_chars": " ."
}
```

- `mappings`：逐段应用的折叠映射，贪心最长匹配（支持多字符映射如 `ß→ss`）。
- `reserved_names`：保留名，加载时经同一张表折叠；扩展名前的词干也参与匹配
  （`CON.txt` 视为保留）。
- `trim_trailing_chars`：每段末尾需要剥离的字符（Windows 末尾空格/点规则）。

## 冲突分组

输出为最小冲突组（`groups`），每组只包含在同一个折叠键上相撞的条目，
原名原样保留：

- `name-collision`：多个原名折叠到同一路径（含一对多映射）。
- `type-conflict`：同一折叠路径上文件与目录混用，或文件路径同时被用作父目录。
- `violation`：末尾字符被剥离、规范化后为空段、命中保留名。

隐式父目录也会入组：条目 `A/b.txt` 会在节点 `a` 上留下原始前缀拼写 `A`，
与 `a/c.txt` 的 `a` 形成冲突组。

## 条目清单格式

```
F <路径>    # 文件
D <路径>    # 目录
```

空行与 `#` 注释被忽略；路径按原文保留（末尾空格有意义）。

## 测试

```sh
go test ./...
```

合成目录树覆盖：多段折叠、一对多映射与原名保留、父前缀冲突、父目录类型
冲突、末尾空格规则、保留名、多字符映射，以及真实磁盘树的 `ScanDir` 端到端
扫描。

# reprodiff

Python 3.11 可复现性比较库。比较两份目录树（含可嵌套的 ZIP 归档），
产出四类结论和一份可独立复核的证据包。

## 判定

- `identical`：原始输入逐项相同。
- `equivalent`：存在差异，但全部是规则允许忽略的差异，证据中保留原值/新值。
- `different`：存在规则不允许的实质差异。
- `undetermined`：遇到重复 ZIP 条目名、路径穿越、格式损坏或安全额度耗尽，
  无法继续判定（附原因），不会悄悄挑一个条目继续比较。

## 比较维度

- 路径：仅在一侧存在、类型（文件/目录/符号链接）变化。
- 权限：`st_mode` 权限位，文件和目录分别比较。
- 符号链接：比较链接目标字符串。
- 文件内容：SHA-256 不同才读入内容；JSON 按语义比较，其余按字节比较。
- ZIP 条目：条目集合与顺序、压缩方式、权限位、解析器定位的 `date_time`、
  解压后的载荷内容。归档全程在内存中处理，不落盘解压。
- 嵌套 ZIP 受 `max_depth`、累计解压字节数 `max_bytes`、条目数
  `max_entries` 三个额度约束。

## 规则

- `ZipTimestampRule(rule_id, path)`：只忽略 ZIP 解析器定位的条目
  `date_time` 元数据；载荷字节（包括载荷里长得像时间戳/文件头的字节）
  永远逐字节比较，从不修改。
- `JsonKeyRule(rule_id, path, key_path)`：只忽略完整键路径（对象键或数组
  下标）指向的整个子树。数组顺序保留；重复键拒绝；整数保留任意精度，
  小数使用 `decimal.Decimal`，不经过二进制浮点。
- 规则前缀重叠、同路径规则类型冲突、规则目标类型不匹配（如 ZIP 规则指向
  文本、JSON 规则指向归档或目录）直接抛 `RuleError`。

每条忽略记录都包含：规则 ID、字段路径、原值、新值，以及原始对象的摘要
（JSON 为整份文件原始字节 SHA-256；ZIP 时间戳为通过解析器偏移定位的本地
文件头原始字节 SHA-256）。

## 用法

```python
from reprodiff import (
    JsonKeyRule, RuleSet, ZipTimestampRule, Limits,
    compare_trees, save_evidence, load_evidence, verify_evidence,
)

rules = RuleSet([
    JsonKeyRule("build-time", "config.json", ("meta", "build_time")),
    ZipTimestampRule("zip-time", "dist/app.zip"),
])
result = compare_trees("build-a", "build-b", rules=rules,
                       limits=Limits(max_depth=4, max_bytes=64 << 20))
print(result.verdict.value)  # identical | equivalent | different | undetermined

evidence = result.to_evidence()
save_evidence(evidence, "evidence.json")

# 独立复核：只信任两份原始输入、规则和证据包本身
report = verify_evidence("build-a", "build-b", rules, load_evidence("evidence.json"))
assert report.ok, report.problems
```

嵌套归档中的路径使用 `!` 分隔，例如
`app.zip!libs/inner.zip!config.json`。

## 证据包格式

`reprodiff-evidence/1`，确定性 JSON（键排序、无时间戳）：

```json
{
  "format": "reprodiff-evidence/1",
  "verdict": "equivalent",
  "limits": {"max_depth": 4, "max_bytes": 67108864, "max_entries": 10000},
  "inputs": {"left": "sha256:...", "right": "sha256:..."},
  "rules": [{"type": "json-key", "id": "...", "path": "...", "key_path": [...]}],
  "ignored": [{"rule_id": "...", "path": "...", "field_path": "meta.build_time",
               "kind": "value-changed",
               "old": "...", "new": "...",
               "old_digest": "sha256:...", "new_digest": "sha256:..."}],
  "differences": [{"path": "...", "kind": "...", "detail": {}}],
  "undetermined": [{"path": "...", "reason": "..."}],
  "unused_rules": []
}
```

超过 2^53 的整数编码为 `{"@type": "bigint", "value": "十进制字符串"}`，
`Decimal` 编码为 `{"@type": "decimal", "value": "..."}`。

## 验证器的独立性

`reprodiff/verify.py` 不复用主比较器的比较结论或规范化摘要：它从两份
原始目录树重新遍历、重新解压、重新解析 JSON、重新定位 ZIP 本地文件头并
重算摘要，然后与证据包逐节（ignored / differences / undetermined /
unused_rules / inputs / verdict）做多重集合比对。证据被篡改（改值、删记录、
换输入摘要、换结论）都会返回 `ok=False` 及具体原因。

## 测试

```bash
python3.11 -m unittest discover -s tests -t . -v
```

覆盖：嵌套 ZIP 内 JSON 大整数、符号链接目标变化、载荷伪时间戳、重复成员、
解压/深度/条目额度耗尽、路径穿越、损坏归档、重复键、数组顺序、数值精度、
规则重叠与类型不匹配，以及随机受保护字节（必为 `different`）和随机可忽略
字段（保持 `equivalent` 且证据完整）、证据篡改拒绝。

# sensiscan — 敏感字段输出树扫描器（仅合成数据）

对输出树（嵌套 dict/list 的文档）按**指定敏感字段**与**已知标识符字典**扫描，
报告匹配证据但**摘要化原值**。纯 Python 3.11 标准库实现，测试使用 `unittest`。

> **范围声明**：本项目只处理合成数据（`reference_corpus.py` 中所有值均为虚构）。
> 语料标注与测试用于检查漏报、重复证据与误报，属于工程健全性验证；
> 任何统计实验结果都**不作为形式化隐私证明**。

## 运行

```bash
python3.11 -m unittest discover -s tests -v   # 测试
python3.11 run_sample.py                        # 样例：扫描合成语料并交叉核对标注
```

## 参数与定义

- `Rule(rule_id, version, fields, entries)`：具名、带版本的字典规则。
  `fields` 为叶子键名集合（或 `("*",)` 表示所有字符串）；`entries` 为已知敏感标识符。
- `WhitelistEntry(path, rule_id, version)`：白名单绑定**精确 JSON Pointer 路径 +
  精确规则版本**。同名的其他字段不被豁免；规则版本不匹配时白名单失效。
- `Budget(max_strings, max_chars, max_evidence)`：扫描预算。任一预算耗尽即停止，
  报告显式标记 `complete=false` 并给出原因（`incomplete_reasons`）。
- `scan_document(doc, rules, whitelist, budget, chunk_size)`：遍历任意深度嵌套
  （数组元素继承最近祖先字段名，嵌套数组不漏扫），返回脱敏 `Report`。

## 匹配管线

1. **转义解码**（`escapes.py`）：`\uXXXX`（含代理对）、`\xXX`、百分号编码 UTF-8、
   HTML 数字/命名实体；每个解码字符保留其在原文中的字符区间。
2. **规范化路径**（`normalize.py`）：`nfkc_casefold`（NFKC + casefold，覆盖全角、
   大小写变体）与 `nfkc_casefold_desep`（再去除空格/连字符/点/零宽字符等分隔符，
   覆盖被分隔符打散的变体）。字典条目经同一路径规范化后做子串匹配。
3. **流式扫描**（`stream.py`）：按块喂入文本，尾部保留 `HOLD_BACK=40` 字符以解码
   跨块转义；匹配按 `max_pattern-1` 重叠增量进行，跨块匹配恰好命中一次；
   多个规范化路径命中同一原文区间时按区间去重合并（`matched_via` 记录全部路径）。
4. **字节映射**：匹配区间映射回原始字符串的 UTF-8 字节范围（`byte_range`），
   转义序列的命中覆盖该序列本身。

## 证据与脱敏

每条证据包含：路径、规则 ID/版本、命中路径（`matched_via`）、字节范围，以及
原值的**摘要**——截断 SHA-256 指纹（12 位）、长度、首尾各至多 1 字符的掩码预览。
报告与诊断信息均不含敏感原值，报告本身无法完整复刻敏感值
（由 `test_report_does_not_leak_raw_values` 保证）。

## 评估方法（非形式化证明）

`reference_corpus.py` 构造合成嵌套文档与已知变体（全角、零宽、分隔符、百分号、
HTML 实体、`\uXXXX`、嵌套数组、近似但非同值对照），`EXPECTED` 为**独立于扫描器
实现的手工标注**。测试检查：漏报（召回）、多余证据（精确度）、重复证据、
白名单路径/版本绑定、预算耗尽显式未完成、敏感原值不进入报告。

## 文件

- `sensiscan/escapes.py` — 转义解码（保留源区间）
- `sensiscan/normalize.py` — 规范化路径
- `sensiscan/stream.py` — 流式跨块匹配器
- `sensiscan/scanner.py` — 树遍历、规则/白名单/预算
- `sensiscan/report.py` — 脱敏证据与报告
- `reference_corpus.py` — 合成语料 + 独立标注
- `run_sample.py` — 运行样例
- `tests/test_sensiscan.py` — unittest 测试

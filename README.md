# sensiscan — 敏感字段与已知标识符扫描器（合成数据专用）

对 JSON 风格输出树与流式文本做敏感数据检测：按**指定敏感字段**（路径规则）与
**已知标识符字典**（字典规则）扫描，支持规范化变体与路径白名单，报告匹配证据
但对原值做摘要化处理。匹配结果映射回原始字节范围。

> 本工具仅用于合成数据的正确性验证。测试中的统计结果（如合成集上的召回）
> **不构成任何形式化隐私证明**。报告中的截断摘要对低熵取值可被枚举反推，
> 请勿用于真实敏感数据。

## 运行

```bash
python3.11 run_sample.py                      # 运行样例（树扫描 + 流式扫描 + 预算演示）
python3.11 -m unittest discover -s tests -v   # 运行测试（25 个用例）
```

仅依赖 Python 3.11 标准库。

## 定义与参数

### 规则

- `DictRule(rule_id, version, identifiers, pipelines, boundary)`
  已知标识符字典。`identifiers` 会在每条规范化流水线下预规范化后做子串匹配；
  `boundary=True`（默认）要求匹配两侧不是字母数字或下划线，用于控制误报。
- `FieldRule(rule_id, version, path_pattern)`
  敏感字段规则。路径段以 `/` 分隔，`*` 匹配且仅匹配一段（含数组下标）；
  命中路径的字符串无论内容都上报。
- `WhitelistEntry(path, rule_id, rule_version)`
  白名单按 **(精确路径, 规则 id, 规则版本)** 三元组绑定：同名其他字段不豁免，
  版本不匹配不豁免，路径不做后缀/前缀模糊匹配。

### 规范化流水线（`sensiscan.normalize.PIPELINES`）

| 名称 | 步骤 |
|---|---|
| `raw` | 恒等 |
| `nfkc_casefold` | 逐字符 NFKC → casefold |
| `compact` | NFKC → casefold → 去零宽字符 → 去空白 |
| `unescaped_compact` | 解码转义（`\uXXXX`、`\xXX`、`%XX` UTF-8 串、HTML 实体）→ compact |

每条流水线都维护“规范化字符 → 原始字符区间”的映射，因此匹配可映射回
**原字符串的 UTF-8 字节范围**（`Evidence.byte_range`）。一条证据会合并记录
所有命中它的规范化路径（`normalization_paths`）。

### 预算（`ScanBudget`）

- `max_evidence`：证据条数上限；`max_chars`：扫描字符数上限。
- 任一预算耗尽即中止，报告 `status="incomplete"` 并给出 `incomplete_reason`，
  绝不把未完成的扫描标记为完成。

### 证据与摘要化

`Evidence` 只包含：路径、规则 id/版本、类别、字节范围、规范化路径，以及
原值摘要（长度、SHA-256 前 16 个 hex 位、字符类别形状 `9/A/a`）。
**原值本身不进入报告**；测试断言所有种植值及其转义形式都不出现在序列化报告中。

### 流式扫描（`StreamScanner`）

`feed(chunk)` 增量喂入、`finish()` 收尾。保留回看窗口以发现跨块匹配，
字节范围以整条流为坐标。窗口按“转义展开 ≤12 倍 + 128 字符余量”设定；
更夸张的展开（如超长 `%XX` 串，单条转义序列上限 96 字符）不在流式保证
范围内。流式模式只应用字典规则（字段规则面向树路径）。

## 已知限制

- 逐字符 NFKC 不复现跨字符的规范重排（对标识符场景足够）。
- 只扫描字符串叶子；数字/布尔/空值不扫描。
- 字典匹配是精确子串匹配，不做模糊匹配；相似但非同值不应命中（有控制用例）。
- 摘要化是诊断便利而非安全保证；低熵取值可被枚举。
- 合成数据上的召回/误报统计是工程正确性检查，不是形式化隐私证明。

## 布局

- `sensiscan/normalize.py` — 带索引映射的规范化流水线
- `sensiscan/core.py` — 规则、白名单、预算、扫描器、证据报告
- `sensiscan/stream.py` — 流式扫描器
- `sensiscan/synth.py` — 合成参考文档、独立标注与控制样本
- `run_sample.py` — 运行样例
- `tests/test_scanner.py` — unittest 测试（漏报、重复证据、摘要化、白名单、预算、流式）

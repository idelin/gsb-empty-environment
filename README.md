# linediff

Go 行级差异库：输出最少插删行编辑脚本，并提供可校验的应用器。

## 规则

- 脚本由 `Keep` / `Delete` / `Insert` 组成，`Insert+Delete` 数量最少（即 LCS 对齐）。
- 同为最短脚本时，优先保留更早的旧行；仍有并列时，优先保留更早的新行。
  形式化：保留的旧行下标序列在所有最长公共子序列对齐中字典序最小，
  并列时再取保留的新行下标序列字典序最小者。
- 行以 `SplitLines` 切分，每行保留结尾 `\n`；空文件是 0 行，
  `"a"`（末行无换行）与 `"a\n"` 是不同的行，可精确区分。

## 用法

```go
oldLines := linediff.SplitLines(oldText)
newLines := linediff.SplitLines(newText)
script := linediff.Diff(oldLines, newLines)

out, err := linediff.Apply(oldLines, script) // 精确还原 newLines，否则报错
// 或一步到位：s, err := linediff.ApplyString(oldText, script)
```

## 复杂度与测试

`Diff` 使用 O(n·m) 的 LCS 动态规划。测试包含：

- 表格用例：空文件、末行无换行、大量重复行、决胜规则专项。
- `TestExhaustiveShortSequences`：对短序列（字母表 {a,b} 长度 ≤6、
  {a,b,c} 长度 ≤4、{a, "a\n"} 长度 ≤4）的全部序列对，枚举所有最短
  编辑脚本，核对库输出恰为决胜规则下的最优者，并验证应用器精确还原。

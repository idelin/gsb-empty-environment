# renameplanner

文件重命名规划器。所有源路径与目标路径都按**初始目录状态**解释：
`a→b, b→c, c→a` 表示“当前叫 a 的文件最终叫 b”，依此类推。

## 设计

- **图分解**：源、目标各自唯一时，移动关系构成若干链与环。
  - 链（末端目标空闲）：逆序执行，**无需临时名**。
  - 环（如 a→b→c→a）：先用一个临时名把环上任意一个文件挪开，
    依次归位，最后临时名落位。**每个环恰好一个临时名，即理论最小值**。
- **自改名** `a→a` 是空操作，直接丢弃。
- **临时名**形如 `.rename-tmp-N-i`，保证不与初始文件、任何源/目标、
  其他临时名冲突。
- **校验**：源不存在、源/目标重复、目标被未参与文件占用，均报错。
- **独立核对**：`Verify` 在“路径→文件身份”的虚拟目录上模拟整个计划，
  确认每个文件身份落到正确目标，其余文件不受影响。
- **可恢复日志**：`Execute` 每完成一步就向 journal 追加一条 `D` 记录
  并 fsync；中断后用同一计划重新 `OpenJournal` 即可从断点续跑。
  已完成的步骤会被跳过；journal 与计划不匹配时拒绝执行。

## 用法

```go
plan, err := rename.PlanMoves(moves, func(p string) bool { /* 初始目录中 p 是否被占用 */ })
if err != nil { log.Fatal(err) }
if err := rename.Verify(initial, moves, plan); err != nil { log.Fatal(err) } // 独立模拟核对
j, err := rename.OpenJournal("rename.journal", plan)
if err != nil { log.Fatal(err) }
err = rename.Execute(plan, fs, j) // 失败后用同一计划重跑即可续传
```

## 测试

```sh
go test ./...
```

覆盖：自改名、链、单环、多环+链混合、临时名避让、目标被占用/源缺失/
重复源目标报错、注入部分失败后的日志恢复、200 组随机置换的独立模拟核对
（并断言临时名数量 == 环数）。

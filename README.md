# errparse — 预测表解析 + FOLLOW 同步恢复 + 最小编辑修复森林

纯 Python 3.11+（仅标准库），不依赖任何现成正则/自动机库。
字母表与转移结构以显式字典输入，文法以显式产生式输入。

## 功能与需求映射

- **无冲突预测表解析**：`errparse/grammar.py` 计算 FIRST/FOLLOW 并构建
  LL(1) 预测表；任何冲突（左递归、悬吊 else 等）在建表时以
  `TableConflict` 拒绝，不会生成有冲突的表。
- **显式 DFA 词法器**：`errparse/lexer.py` 的 `DFA(alphabet, transitions,
  start, accepts)` 全部为显式输入，最大 munch 切分。
- **FOLLOW 同步恢复**：`errparse/parser.py`
  - 缺终结符：报告期望集合并插入占位 token（`origin="inserted"`，
    独立 uid），不消耗输入；
  - 意外 token：在 FOLLOW/never_skip 集合上同步弹栈（不消耗 token），
    否则把 token 跳进到**错误节点**保留在树中（按正确的兄弟位置插入），
    绝不吞掉后续合法结构；
  * 每个错误动作要么消耗输入要么缩小栈，多处错误不会无限弹栈；
    每个输入位置最多报告一次。
- **空产生式建树**：ε 产生式生成显式 `epsilon` 叶节点。
- **预算内最小编辑修复**：`errparse/repair.py` 在 (输入位置, 栈) 配置上做
  单位成本 Dijkstra（insert/delete/substitute 各 1），返回**共享修复候选
  森林** `RepairForest`——所有同成本修复都保留在森林中，不任意装作唯一；
  `enumerate()`/`count()` 给出全部去重后的候选脚本。
- **token 身份与源码映射**：`errparse/tokens.py` 中 source / inserted /
  substituted token 有不同 uid 前缀与 `origin`；`apply_edits` 应用修复后
  原 token 保持身份与源码位置，树叶子可映射回源码偏移。
- **短串枚举核验**：`errparse/enumerate_edits.py` 对短 token 串按显式字母表
  暴力枚举 ≤k 的编辑结果，用普通解析器判定接受，作为最小成本的独立参考
  （`tests/test_enumerate.py` 交叉核验）。
- **增量解析**：`errparse/incremental.py` 按块（语句）解析；token 修改后
  用 token 级 diff 复用未受影响块的树（按对象身份复用），只重解析相关块；
  块内恢复绝不跳过可开始新块的 token，因此坏块不会吞掉合法后续结构。
- **反例可独立执行**：`counterexamples/` 下三个脚本各自 `python3` 直接运行。

## 运行

```bash
python3 run_demo.py                          # 端到端样例
python3 -m unittest discover -s tests        # 测试（31 个）
python3 counterexamples/ce01_left_recursion.py
python3 counterexamples/ce02_naive_swallow.py
python3 counterexamples/ce03_duplicate_reports.py
```

## 样例输出（节选）

`y = x + ;` 的最小修复森林——成本 1 的三个候选全部保留：

```
minimal cost: 1 | candidates: 3
  repair: delete PLUS at position 3
  repair: insert ID before position 4
  repair: insert NUM before position 4
repaired stream re-parses cleanly: True
```

错误恢复保留跳过 token、不吞后续结构（`print ( x ;` / `if y = 3 ;` / `z = 4 ;`）：

```
  pos 3: missing: expected RPAREN, found 'SEMI'
  pos 6: unexpected: expected ID/IF/PLUS/PRINT/RPAREN/SEMI, found 'ASSIGN'
  pos 7: unexpected: expected ID/IF/PLUS/PRINT/RPAREN/SEMI, found 'NUM'
  pos 8: unexpected: expected ID/IF/PRINT, found 'SEMI'
program
  stmt_list
    stmt
      PRINT 'print' [src-6 @0 source]
      expr
        term
          LPAREN '(' [src-7 @6 source]
          expr
            term
              ID 'x' [src-8 @8 source]
            expr_tail
              epsilon
          RPAREN '<RPAREN>' [ins-0 @10 inserted]     # 插入 token 独立身份
        expr_tail
          epsilon
      SEMI ';' [src-9 @10 source]
    stmt_list
      stmt
        IF 'if' [src-10 @12 source]
        expr
          term
            ID 'y' [src-11 @15 source]
          <error:<error>>                             # 跳过的 token 保留在错误节点
            ASSIGN '=' [src-12 @17 source]
          <error:<error>>
            NUM '3' [src-13 @19 source]
          expr_tail
            epsilon
        <error:<error>>
          SEMI ';' [src-14 @21 source]
        stmt                                          # z = 4 ; 完整保留
          ID 'z' [src-15 @23 source]
          ASSIGN '=' [src-16 @25 source]
          expr
            term
              NUM '4' [src-17 @27 source]
            expr_tail
              epsilon
          SEMI ';' [src-18 @29 source]
      stmt_list
        epsilon                                       # 空产生式显式建树
```

增量更新（4 条语句中改第 2 条）：

```
initial: 4 blocks, ok=True
edit value in stmt 2: reused=3 reparsed=1
stmt 1 tree reused by identity: True
break stmt 2 (`b = ;`): reused=3 reparsed=1 errors=1
  pos 6: unexpected: expected ID/LPAREN/NUM, found 'SEMI'
stmt 3 still intact: ['ID', 'ASSIGN', 'NUM', 'SEMI']
```

## 测试结果

```
$ python3 -m unittest discover -s tests
Ran 31 tests in ~5s
OK
```

覆盖：FIRST/FOLLOW/建表与冲突拒绝、显式 DFA 词法（含 LexError 定位）、
干净解析与源码映射、空产生式建树、缺终结符期望集合、错误节点保留跳过
token、不吞后续合法结构、多错误终止且位置不重复、修复森林最小成本与
多候选、修复后身份/源码映射、预算约束、短串暴力枚举交叉核验、增量
块失效与复用。

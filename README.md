# csp_audit — 可审计的 CSP 求解器（Python 3.11，仅标准库）

有限域 CSP 求解器，**不调用任何 SAT/SMT 库**。求解过程产生完整审计轨迹
（trail），独立重放检查器逐步验证每个事件是否由规则支持；SAT 结论由独立
检查器对照**原始约束**复核。

## 结构

- `csp_audit/model.py` — `Problem` / `Constraint`（谓词约束，有限域）
- `csp_audit/trail.py` — 事件类型：`DECIDE` / `PROPAGATE` / `CONFLICT` / `BACKTRACK`
- `csp_audit/solver.py` — 回溯搜索 + GAC 传播，每次域变动都记录带理由的事件；
  SAT 前先用独立检查器验证；步数超限返回 `UNKNOWN`，**绝不包装成 UNSAT**
- `csp_audit/replay.py` — 独立重放检查器：从全新域状态逐事件重放并验证规则；
  发现篡改时报告**首个非法事件**的下标
- `csp_audit/verify.py` — 独立 SAT 检查器：完整性（每变量恰好一个值）、域内、
  满足全部原始约束

## 重放规则（每步必须有规则支持）

- `DECIDE(var,value,level)`：level 等于当前决策深度；value 在当前域中
- `PROPAGATE(var,value,cid,level)`：cid 是真实约束且 var 在其作用域内；
  value 在当前域中；且在该约束下**无支持**（有支持的删除 = 无理由删值 → 失败）
- `CONFLICT(var,cid,level)`：var 的域此刻确实为空
- `BACKTRACK(var,value,level)`：必须匹配最内层未决决定，且该子树带有已验证的
  失败证据（发生过已验证的 CONFLICT，或下一层变量的所有值均已尝试并回溯）；
  检查器恢复**自己的**快照并删除已试值（回溯恢复快照的正确性由此被独立检验）
- 终态判定：`SAT` 要求所有域收缩为单例（完整赋值）并通过 `check_sat`；
  `UNSAT` 要求无未决决定且（根冲突或某根域被耗尽）；`UNKNOWN` 不作结论

## 运行

```bash
python3.11 main.py                 # 演示：4 皇后(SAT)、K4 三色(UNSAT)、限步(UNKNOWN)
python3.11 -m unittest discover -s tests -v
```

## 运行样例（`python3.11 main.py` 摘要）

```
=== 4-queens (SAT) ===
status: SAT, trail events: 23
assignment: {'q0': 1, 'q1': 3, 'q2': 0, 'q3': 2}
independent check_sat: True
replay: ok=True verdict_supported=True (SAT verified against original constraints)

=== K4 3-coloring (UNSAT) ===
status: UNSAT, trail events: 51
replay: ok=True verdict_supported=True (UNSAT supported by trail)

=== 8-queens with max_events=3 (UNKNOWN, not UNSAT) ===
status: UNKNOWN, trail events: 4
replay: ok=True verdict_supported=False (no verdict claimed; events legal)
```

## 测试结果

`python3.11 -m unittest discover -s tests -v`：**18 个测试全部 OK**，覆盖：

- SAT 路径：独立检查器验证完整赋值；坏赋值（违约束/不完整/域外/多变量）被拒；
  不完整赋值不能声称 SAT
- UNSAT 路径：轨迹支持 UNSAT；根冲突支持 UNSAT；`UNKNOWN` 不等于也不能伪装成
  UNSAT
- 重放规则：无理由删除域值失败；引用不存在约束失败；决定值不在域中失败；
  假冲突失败；无失败证据的回溯失败；回溯与决定不匹配失败
- 篡改检测：篡改传播理由、篡改传播值、删除事件，均定位到**首个非法事件**下标；
  用 SAT 轨迹伪造 UNSAT 失败
- 快照：保存/恢复往返一致；失败分支中被剪的值在回溯恢复后会被再次剪除
  （只有恢复正确才可能），整条含回溯的轨迹重放通过

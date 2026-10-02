# flowdeps — 字段级精度的增量依赖追踪引擎

纯 Python 3.11 标准库实现（测试用 `unittest`）。步骤读取若干**输入版本**并
产出**输出版本**（内容指纹）；输入修订后，引擎以字段级精度传播失效，给出
直接/传递依赖区分、最短解释链、批量报告、完整重执行参考检查与可重放证据。

## 运行

```bash
python3.11 sample.py                  # 合成数据演示
python3.11 -m unittest discover -s tests -v   # 23 个测试
```

## 结构

- `flowdeps/model.py` — 稳定 ID + 版本：事实/规则/步骤、`Change`、五类依赖边
- `flowdeps/graph.py` — 依赖图：撤边、拓扑序、环诊断（`CycleError`）
- `flowdeps/engine.py` — 执行、依赖记录、失效传播、影响查询、批量提交
- `flowdeps/reference.py` — 完整重执行参考检查
- `flowdeps/evidence.py` — 事件日志重放
- `sample.py` — 合成数据样例；`tests/test_flowdeps.py` — 测试

## 需求映射

| 需求 | 实现 |
| --- | --- |
| 步骤读输入版本、产输出版本 | `Reader` 记录带版本的边；输出指纹即版本 |
| 查询输入修订影响、直接/传递、最短链 | `Engine.impact()` → `ImpactReport(direct, transitive, chains)` |
| 区分实际读字段/条件分支/负依赖 | `FieldDep` / `BranchDep` / `NegativeDep`（另有 `RuleDep`、`StepDep`） |
| 新增原本不存在的输入触发失效 | `NegativeDep` 在 `old_version is None` 的新增事件上触发 |
| 步骤更正撤回旧边 | 重执行时 `DepGraph.install` 先撤旧边再装新边 |
| 循环失效不能自我支撑 | 失效只从真实 `Change` 根传播；输出不变即截断（revalidated） |
| 批量变更最小解释路径 + 重新验证节点 | `BatchReport.explanations`（BFS 最短链）、`revalidated` |
| 完整重执行参考检查 | `reference_check`：无漏失效 + 无关分支访问数（陈旧边） |
| 事实/规则/步骤稳定 ID 和版本 | `FactState` / `RuleState` / `StepDef` 均带 version |
| 证据可重放 | `Engine.log` + `replay()` 重建并逐条比对日志 |
| 旧版本消费者不自动依赖新版本 | 边钉住读取时版本；仅实际读字段变更才失效（跨版本亦捕获） |
| 未消费变更字段的步骤不受影响 | 字段级 `changed_fields` 匹配 |
| 环状非法处理链须诊断 | 执行期栈检测 + 提交前图检测，抛 `CycleError(chain)` |

## 样例输出（`python3.11 sample.py`）

```
== 1. initial build (synthetic data) ==
  user:1: bonus=100 eu=0.9 us=1.1
  user:2: ineligible
  waitlist:user:3=False

== 2. impact query: revision of user:1 [age] ==
  direct:     ['eligibility']
  transitive: ['report']
  shortest chain to eligibility: user:1 -> eligibility
  shortest chain to report: user:1 -> eligibility -> report

== 3. batch: unconsumed fields + output-neutral change ==
    roots: ['eligibility']   affected: []   revalidated: ['eligibility']

== 4. batch: user:2 crosses the age gate ==
    affected: ['eligibility', 'report'] (direct=['eligibility'], transitive=['report'])
    explain report: update user:2 v2->v3 [age] -> eligibility -> report

== 5. negative dependency: add previously missing user:3 ==
    explain eligibility: add user:3@v1 -> eligibility

== 6. batch of two changes: minimal explanation paths ==
    roots: ['eligibility', 'pricing']   affected: all three
    explain report: rule bonus v1->v2 -> eligibility -> report

== 7. step correction retracts old edges ==
    pricing corrected v1->v2; 此后 rate:us 变更: roots=[] affected=[]

== 8. illegal cyclic processing chain ==
  diagnosed: illegal cyclic processing chain: cycle-x -> cycle-y -> cycle-x

== 9. reference check ==
  missed invalidations: []   irrelevant branch reads: 0   ok: True

== 10. evidence replay ==
  entries: 40, identical: True
```

## 测试结果

```
Ran 23 tests in 0.003s
OK
```

23 个用例覆盖：影响查询与最短链、字段精度、分支精度、负依赖、旧版本
语义、步骤更正撤边、环诊断、失效不自支撑、批量最小解释与重验证节点、
参考检查（含注入陈旧边被捕获）、证据重放。

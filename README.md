# 事务变更缓冲与原子应用器

Python 3.11 标准库实现（无第三方依赖、不接消息平台）。变更来自本地夹具
`fixtures.py`，每条变更带事务 ID、表、主键、前值/后值；提交标记带全局提交序号
和该事务的变更条数。

## 语义

- **同事务缓冲**：同一 `txid` 的多条 `Change` 先按事务内序号 `seqno` 缓冲，
  未提交前对外完全不可见。
- **提交标记早到**：`Commit` 先于变更到达时进入 `waiting`，直到收齐
  `change_count` 条且事务内序号 1..N 连续无缺口，事务才可提交。
- **原子应用**：提交时在影子副本上按序“校验前值 + 应用变更”，全部成功后
  一次性原子落盘（临时文件 + `os.replace` + `fsync`）再切换内存状态；
  任一条前值不匹配则丢弃影子副本，已提交数据与磁盘均不变（全有或全无）。
- **先删后插同键**：同一事务内按 `seqno` 顺序执行，后续变更的前值能看到
  前面变更的结果，因此“删除同键再插入同键”最终为插入后的新值。
- **提交序号缺口暂停**：就绪事务严格按 `Commit.commit_seq` 连续提交；
  缺号时流水线 `paused`，后续序号（即使已就绪）也不可见；缺号补齐后
  自动连续续提。
- **回滚丢弃**：`Abort` 删除缓冲（含因缺口暂存在就绪队列里的事务）；
  已提交事务的 abort 返回 `duplicate`，不影响数据。
- **重复不二次生效**：已提交 `txid` 的重复变更/重复提交标记一律忽略；
  重复但内容不一致（同序号不同负载、不同提交序号）抛出 `ValueError`。
- **半事务恢复不可见**：只有已提交快照写入 `state.json`；重启后缓冲、
  等待清单、就绪队列全部消失，半事务与暂停事务必须由上游重传补齐。

## 目录

- `txbuffer/applier.py` — 事件类型 `Change/Commit/Abort` 与 `Applier`
- `fixtures.py` — 本地夹具事件流（乱序、早到、缺口、回滚、重复、重启重传）
- `demo.py` — 运行样例（含“崩溃—重启—重传”）
- `tests/test_applier.py` — `unittest` 套件（18 个用例）
- `results/` — 保存的运行样例与测试输出

## 运行

```bash
python3.11 demo.py            # 自动使用临时状态目录
python3.11 demo.py ./state    # 指定目录，可查看 state/state.json
python3.11 -m unittest discover -s tests -v
```

## 运行样例（节选）

```
Commit tx=T6 commit_seq=6 变更数=1                    -> 就绪但遇序号缺口，暂停
Commit tx=T3 commit_seq=3 变更数=2                    -> 等待清单
Commit tx=T1 commit_seq=1 变更数=2                    -> 已原子提交
Commit tx=T2 commit_seq=2 变更数=2                    -> 已原子提交
Change tx=T3 seq=2 accounts[2] 更新                  -> 已原子提交
Commit tx=T4 commit_seq=4 变更数=2                    -> 等待清单
Abort  tx=T5                                         -> 回滚丢弃
Commit tx=T1 commit_seq=1 变更数=2                    -> 重复，忽略
--- 实例 1 状态 ---
last_commit_seq = 3        # T4 半事务、T6 缺口暂停，均不可见
paused = True

# 重启后：只剩 T1/T2/T3，T4 缓冲消失
Change tx=T4 seq=2 accounts[3] 更新                  -> 已原子提交
Commit tx=T7 commit_seq=5 变更数=1                    -> 已原子提交
Commit tx=T6 commit_seq=6 变更数=1                    -> 已原子提交
--- 实例 2 最终状态 ---
last_commit_seq = 6
paused = False
  重放 T1 提交标记 -> 重复，忽略；last_commit_seq 仍为 6
```

完整输出见 `results/demo_output.txt`。

## 测试结果

```
Ran 18 tests in 0.010s

OK
```

覆盖：未提交不可见、整事务原子提交、同键先删后插、提交标记早到等清单、
事务内序号缺口等待、提交序号缺口暂停/自动续提、乱序提交按序应用、
重复事务幂等、重复负载冲突拒绝、前值不匹配整事务回滚、回滚丢弃（含暂停中事务）、
重启后半事务不可见、主键类型持久化保持、重启后重传续提。
完整输出见 `results/test_output.txt`。

# ormap — 观察删除多值映射（observed-remove multi-value map）

每次赋值产生唯一 `dot=(replica,counter)`；删除只携带调用时已观察到的该键 dots，
并发未观察的赋值存活。读取保留全部存活赋值，展示值按逻辑时间、再按 replica ID
决胜，且不为展示删除任何并发值。操作与状态均可合并（交换、结合、幂等）；
同 dot 不同内容会被拒绝。checkpoint 导出/恢复保留压缩墓碑（每副本计数器区间），
足以阻止旧 dot 复活。

## 运行

```sh
go test ./...        # 单元 + 穷举测试（三副本全排列 × 重复投递 × 压缩后重放）
go run ./cmd/demo    # 本地合成事件样例，打印存活集合/展示值/冲突标记
```

## 结构

- `ormap/ormap.go` — Dot/Version/Op、Assign/Observe/Remove/Apply/Merge、Live/Display/Conflict
- `ormap/checkpoint.go` — Checkpoint 导出与恢复（墓碑压缩为计数器区间）
- `ormap/reference.go` — 操作集合的直接解释，作为测试基准
- `ormap/ormap_test.go` — 语义单元测试
- `ormap/exhaustive_test.go` — 三副本穷举：删除前后可见内容、逻辑时钟并列、
  全排列+重复投递对照参考、状态合并收敛、压缩后旧消息重放

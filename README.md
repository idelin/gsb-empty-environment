# DAG 构建任务调度器

在 CPU / 内存容量约束下，为带依赖关系（DAG）的不可抢占任务生成调度计划。
使用**确定性启发式**算法给出工期上界，并同时输出**下界**用于评估间隙。
输入全部来自本地 JSON 文件，不联网查包。

> 注意：启发式工期是可行上界，**不代表全局最优**；下界用于衡量与最优解的可能差距。

## 运行环境

- Python 3.11，仅使用标准库
- 测试框架：`unittest`

## 输入格式（本地 JSON）

```json
{
  "name": "demo-build",
  "capacity": {"cpu": 4, "memory": 8},
  "packages": [{"name": "openssl", "version": "3.1.0-rc1"}],
  "tasks": [
    {"id": "fetch", "cpu": 1, "memory": 1, "duration": 2, "deps": []}
  ]
}
```

- `capacity`：整机 CPU / 内存容量（正整数）。
- `packages`：本地包清单，`version` 必须为 `major.minor.patch`，可带 `-预发布`
  与 `+构建元数据`（如 `3.1.0-rc1`、`3.26.4+build.2`），非法版本号直接报错。
- `tasks`：`cpu` / `memory` 为任务资源需求，`duration` 为不可抢占时长（正整数），
  `deps` 为前置任务 id 列表。

## 调度规则

- 任务不可抢占，一旦开始连续运行 `duration` 个时间单位。
- 任意时刻，运行中任务的 CPU 之和、内存之和均不得超过容量；
  因此"可同时运行但内存之和超限"的任务不会被同排。
- 单个任务需求超过容量时，问题直接判定不可行（退出码 2）。
- 启发式：事件驱动的并行列表调度，优先级为关键路径位次降序、任务 id 升序，
  同一输入必得同一结果（确定性）。
- 下界：`max(关键路径长度, ⌈CPU 总工作量 / CPU 容量⌉, ⌈内存总工作量 / 内存容量⌉)`。

## 使用方法

```bash
python3.11 -m scheduler examples/sample_build.json          # 文本输出
python3.11 -m scheduler examples/sample_build.json --json   # JSON 输出
python3.11 -m unittest discover -s tests -v                 # 运行测试
```

## 运行样例

```
$ python3.11 -m scheduler examples/sample_build.json
构建: demo-build
容量: cpu=4, memory=8
本地包:
  - zlib 1.2.13
  - openssl 3.1.0-rc1
  - cmake 3.26.4+build.2
调度计划:
  任务                  开始    结束
  fetch                0     2
  unpack               2     3
  configure            3     5
  compile_core         5    10
  compile_ui           5     8
  compile_io          10    14
  test_core           10    12
  test_io             14    16
  package             16    17
启发式工期 (makespan): 17
下界 (lower bound):    13
间隙 (gap):            4
注意：本工期由确定性启发式算法给出，是可行上界，未证明为全局最优。
```

不可行输入（单任务内存 16 超过容量 8）：

```
$ python3.11 -m scheduler examples/infeasible.json
错误: 单任务资源需求超过容量，问题不可行: huge_task
（退出码 2）
```

## 测试结果

```
$ python3.11 -m unittest discover -s tests -v
test_capacity_never_exceeded ... ok
test_critical_path_priority ... ok
test_dependencies_respected ... ok
test_deterministic ... ok
test_memory_limit_prevents_coscheduling ... ok
test_single_task_over_capacity_infeasible ... ok
test_bad_version_rejected ... ok
test_cycle_detected ... ok
test_duplicate_task_id ... ok
test_load_example ... ok
test_load_from_json_file ... ok
test_unknown_dependency ... ok
test_critical_path ... ok
test_lower_bound_never_exceeds_makespan ... ok
test_resource_bound ... ok
test_invalid_versions ... ok
test_parse_and_order ... ok
test_valid_versions ... ok

Ran 18 tests in 0.013s
OK
```

## 代码结构

- `scheduler/loader.py`：本地 JSON 加载与校验（版本号、重复 id、未知依赖、环检测）
- `scheduler/semver.py`：`major.minor.patch` 版本校验与排序（支持预发布）
- `scheduler/heuristic.py`：确定性列表调度启发式 + 单任务超容量可行性检查
- `scheduler/lowerbound.py`：关键路径 / 资源负载下界
- `scheduler/cli.py`：命令行入口
- `examples/`：可行与不可行两个示例输入
- `tests/test_scheduler.py`：18 个 unittest 用例

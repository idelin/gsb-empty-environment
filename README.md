# hexgrid — 分区存储的平面轴坐标六边形网格查询

纯离散六边形网格（轴坐标 `Hex{Q,R}`），不做经纬度/地球投影假设。
六个邻居偏移固定为 `(1,0) (1,-1) (0,-1) (-1,0) (-1,1) (0,1)`（见 `hex.go` 的 `Directions`）。

## 设计

- **分区只是存储边界**：`Store` 按 `chunkSize` 把 (q,r) 空间切成矩形分区（负坐标用向下取整除法），邻接关系全局一致，跨区移动无任何特殊处理。
- **按页加载**：查询按需 `LoadLatest`/`LoadAt` 拉取分区页并缓存；页中只含已知（有限）单元，未知坐标一律视为不可通行，绝不拼出不存在的地图。分区返回缺页（`ErrPageMissing`）时查询直接失败。
- **版本一致性**：版本为 `{Data, Config}`。查询开始时钉住（pin）版本；任何页返回的版本戳与钉住版本不一致（异步加载期间发生批量更新或配置换版）即整轮重试，重试耗尽返回 `ErrVersionConflict`。显式旧版本查询（`Query.Version`）读取保留的历史快照，不受并发更新影响。
- **批量更新**：`Store.BatchUpdate` 原子提交并产生新数据版本，旧快照全部保留。
- **字典序决胜路径**：BFS 逐层扩展，同层按“父路径字典序 + 邻居坐标字典序”放松，每个可达单元记录最短步数下字典序最小的一条路径。
- **参考实现**：`ReferenceBFS` 在不分区的平面单元表上跑同一核心算法，测试用它交叉验证分区引擎。

## 查询语义

- `Query{Start, K, Target?, Version?}` → `Result{Version, Reachable, Dist, Steps, Path, Stats}`。
- `K=0` 只返回可通行起点；起点是障碍或不在有限地图内返回 `ErrStartBlocked`。
- `Target` 在 K 步内不可达时 `Steps=-1`、`Path` 为空。
- `Stats.PageLoads` 记录本次查询的按页加载次数（访问量），`Stats.Retries` 记录版本冲突重试次数。

## 测试与回放

```sh
go test ./...        # 全部单测（含 50 组随机地图对拍参考实现）
go test -v -run TestReplayJSON .   # 重放 testdata/replay.json 并打印各查询访问量
go run ./cmd/genreplay             # 重新生成 testdata/replay.json
```

覆盖场景：跨多分区、负坐标、K=0、障碍起点、被障碍围住、分区缺页、
数据换版重试/拒绝、配置换版、批量更新与旧版本查询、字典序决胜。

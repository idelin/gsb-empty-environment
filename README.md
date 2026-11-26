服务网格数据面通过异常点摘除（Outlier Detection）自动隔离故障上游实例，同时开启连续 5xx 摘除、连续本地源错误摘除与周期成功率标准差统计摘除，并受最大摘除比例帽（`maxEjectionPercent`）保护以防级联雪崩；多触发器叠加退避倍增时长与恢复衰减时极易算错生效摘除数与解禁时刻。为数据面健康控制层交付可导入的 Go 异常点摘除状态机包。输入上游主机池与配置（连续 5xx 阈值、连续网关错误阈值、统计周期、最小主机数 `successRateMinHosts`、最小请求数 `successRateReqVolume`、标准差因子 `stdevFactor`、`baseEjectionTime`、`maxEjectionTime`、`maxEjectionPercent`）及按毫秒到达的请求结果和定时统计周期事件。规则：任一主机在连续错误达标或统计周期内成功率严格低于 `mean - (stdev * stdevFactor / 1000)`（仅当样本 ≥ volume 的有效主机数 ≥ minHosts 时计算均值与总体标准差）时触发摘除判定；仅当当前已摘除主机占比严格低于 `maxEjectionPercent`（或当前 0 台被摘除且比例上限 > 0 时允许摘除首台）才实际摘除；每次摘除递增该主机惩罚倍数 `ejectionCount` 并设时长为 `min(maxEjectionTime, baseEjectionTime * ejectionCount)`；到期自动恢复且若主机连续健康运行满一个完整周期无新摘除则按退避衰减规则递减 `ejectionCount`。验收用确定性毫秒事件流逐事件核对每台主机的摘除状态、精确恢复时刻与统计阈值，覆盖 `maxEjectionPercent` 拦住超额雪崩摘除、有效主机数不足跳过标准差统计、连续两次摘除时长翻倍触顶 `maxEjectionTime`，以及恢复后健康运行衰减惩罚倍数。

## 交付物

状态机包位于 `outlier/`，导入路径 `prescreen.local/ps-f17c15a726/outlier`，仅依赖标准库。

### 核心 API

- `New(hosts []string, cfg Config) (*Detector, error)`：校验配置并按主机池顺序构造状态机。
- `(*Detector).ApplyResult(RequestResult) (*Outcome, error)`：投递毫秒级请求结果，惰性执行到期恢复，更新连续 5xx / 本地源错误链与窗口统计，达标时发起摘除判定。
- `(*Detector).Tick(timeMs int64) (*TickReport, error)`：统计周期事件——先到期恢复，再按总体标准差做成功率摘除，然后执行退避衰减并滚动窗口。
- `(*Detector).Advance(timeMs int64) ([]Recovery, error)`：仅推进时钟并回收到期主机，不触发统计。
- `IsEjected` / `EjectedCount` / `Host` / `Hosts`：逐主机查询摘除状态、精确解禁时刻与各项计数器快照。

### 关键语义

- 触发器：连续 5xx、连续本地源（网关）错误、周期成功率低于 `mean - stdev*stdevFactor/1000`（严格小于，等于不摘除）。
- 统计门控：仅当周期内请求数 `>= successRateReqVolume` 的有效主机数 `>= successRateMinHosts` 时才计算均值与总体标准差，否则整个周期跳过统计。
- 比例帽：当前摘除占比严格低于 `maxEjectionPercent` 才放行；0 台已摘除且上限 >0 时首台恒允许；`maxEjectionPercent=0` 完全关闭摘除。被拦截的判定通过 `BlockedEjection` 返回。
- 退避时长：第 n 次摘除时长为 `min(maxEjectionTime, baseEjectionTime * n)`，解禁时刻 = 摘除时刻 + 时长，到期（含恰好相等）自动恢复。
- 退避衰减：恢复后连续健康运行满一个完整 `Interval` 且期间无新摘除，则每个周期 `ejectionCount` 递减 1；期间再次摘除会重置健康计时。
- 事件必须按 `TimeMs` 非递减投递，时间倒退与未知主机均返回错误。

### 测试

`outlier/outlier_test.go` 使用确定性毫秒事件流逐事件断言，覆盖比例帽拦截（含首台特例与 cap=0）、有效主机不足跳过统计、阈值严格性与标准差公式数值、网关错误链与成功重置、时长翻倍触顶、精确恢复时刻、多级衰减与再摘除重置。运行：

```sh
go test ./... -count=1 -v
```

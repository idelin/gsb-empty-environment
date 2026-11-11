# 命名空间用户映射组合

隔离运行器通过多层用户命名空间映射身份，区间重叠或部分未映射可能造成宿主身份误判。写供Go安全宿主调用的映射包，每层由容器起点、父层起点、长度三元组组成，两个方向都必须无重叠且不得溢出；组合多层映射，返回指定容器ID区间在宿主的精确分片及未映射空洞。更新整链原子发布，查询固定版本。用缩小ID空间逐点对照，拒绝把空洞映射为零。

## 包结构（`package netsec`）

- `Entry{ContainerStart, ParentStart, Length}`：单层三元组，半开区间，恒等偏移映射。
- `NewLayer(...)`：校验后返回不可变 `Layer`；拒绝零长度、越过 `uint32` 空间（64 位运算，永不回绕）以及容器侧或父侧的任何区间重叠。
- `Chain.Publish(layers...)`：以 `atomic.Pointer` 原子发布整条层链，返回不可变 `Version`；版本号单调递增，旧版本永久可查，查询始终固定在调用方持有的版本上。
- `Version.MapInterval(start, length)`：逐层组合，返回 `MapResult{Shards, Holes}`，两者恰好覆盖查询区间，空洞不会被翻译。
- `Version.MapID(id) (hostID, mapped, err)`：单点查询强制返回 `mapped` 布尔值；空洞返回 `mapped=false`，绝不可能被误读为宿主 ID 0（合法映射到 0 仍正常返回 `mapped=true`）。

## 验证

- `mapping_test.go`：校验拒绝路径、单层与确定性多层组合（含合法映射到 0 与相邻空洞）。
- `mapping_fuzz_test.go`：在缩小的 48 个 ID 空间内随机生成双向无重叠的 1–3 层链，300 组试验对每个子区间和每个 ID 与独立暴力 oracle 逐点对照，并检查分片/空洞划分不变量。
- `mapping_chain_test.go`：原子发布与并发读写（`-race`）、`2^32-1` 边界算术、防御性拷贝。

运行：`go test -race -count=1 ./...`

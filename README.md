# snaprecv — 乱序分块快照接收库

接收按偏移乱序到达的快照分块，完整校验前只写暂存区，安装时原子替换可见状态。

## 磁盘布局

```
root/
  current.json                # 可见状态指针 {"version": N}，rename 原子替换
  snapshots/v<N>              # 不可变的已安装快照
  staging/v<N>/manifest.json  # 清单：Meta + 逐块已校验标记
  pool/<sha256hex>            # 已校验块，内容寻址，跨版本共享
```

## 保证

- **暂存隔离**：`WriteChunk` 只写 `pool/` 与 `staging/`，可见状态仅在 `Install` 中经两次原子 rename 改变。
- **版本单调**：`Offer` / `WriteChunk` / `Install` 对 `version <= 已安装` 一律返回 `ErrStaleVersion`，旧版本响应无法覆盖新快照。
- **逐块校验**：每块按清单 SHA-256 与期望长度校验（尾块允许更短但长度必须精确）；哈希不符的冲突块被拒绝且不动已校验块。
- **完整校验**：`Validate` 重算整体 SHA-256；`Install` 要求所有块已校验。
- **原子安装**：拼装 `snapshots/tmp-*` → fsync → rename 为 `v<N>` → 写指针临时文件 → fsync → rename `current.json` → 目录 fsync。任意时刻崩溃，可见状态要么是旧快照、要么是新快照，绝不撕裂。
- **崩溃恢复**：`Open` 重放清单，已校验块（池 + 标记）直接复用，无需重新投递或重新哈希；过期版本的暂存被清理，中断安装的临时文件被回收。

## 故障注入

`Open(dir, hook)` 的 `Hook` 在五个切点（`CutPoints()`）被调用：块 rename 前、清单 rename 前、快照 rename 前、指针 rename 前、指针 rename 后。返回错误即模拟该瞬间掉电；随后应丢弃 `Store` 并重新 `Open`。

## 测试

- `store_test.go`：乱序投递、旧版本拒绝、重叠冲突、尾块长度、恢复复用、逐切点安装中断、清单冲突、不完整校验。
- `model_test.go`：逐块模型核对——20 个随机种子 × 400 步随机操作（投递/好块/坏块/校验/安装/随机切点崩溃/重启），每步断言：可见状态恒为某个完整快照、版本不倒退、每个已校验块字节与模型一致、最终收敛到最高版本。

```sh
go test ./... -count=1 -race
```

# 双分区固件更新有限状态检查

这个 Go 程序把双分区 A/B 固件更新协议建成有界有限状态机，并使用广度优先搜索寻找可在断电后启动低于已确认版本的最短事件序列。

## 变量

持久变量在断电后保留：

- `image[A]`、`image[B]`：`empty`、`v1` 或 `v2`。
- `boot_slot`：下一次启动选择的分区，取 `A` 或 `B`。
- `confirmed`：最近一次成功确认的版本，取 `v1` 或 `v2`。
- `fuse`：防回滚熔丝，取 `v1` 或 `v2`，只能在运行态递增。

易失变量在断电后复位：

- `running_slot`：当前运行分区，运行时为 `A` 或 `B`，断电复位为 `none`。
- `running_version`：当前运行版本，运行时为 `v1` 或 `v2`，断电复位为 `empty`。

初始状态为：

- 持久：`image[A]=v1`、`image[B]=empty`、`boot_slot=A`、`confirmed=v1`、`fuse=v1`。
- 易失：`running_slot=none`、`running_version=empty`。

## 迁移规则

- `erase:<slot>`：仅关机、目标非当前启动槽且镜像非空时允许，目标槽变为 `empty`。
- `write:<slot>:<version>`：仅关机、目标非当前启动槽且目标槽为空时允许，写入 `v1` 或 `v2`。
- `set_boot:<slot>`：仅关机且目标槽非空时允许，更新 `boot_slot`。
- `boot`：仅关机时允许；所选镜像为空或版本低于 `fuse` 时启动被拒绝，否则进入运行态。
- `confirm`：仅运行态且运行版本高于 `confirmed` 时允许，把 `confirmed` 更新为运行版本。
- `fuse_increment`：仅运行态且熔丝低于运行版本时允许，熔丝递增一格。
- `power_loss`：仅运行态允许；所有持久变量保留，两个易失变量复位。

## 检查结果

完整 BFS 访问全部 84 个可达状态、探索 192 条合法迁移，找到 6 个违反不变量的启动迁移。最短反例有 7 个事件：

```text
write:B:v2
set_boot:B
boot
confirm
power_loss
set_boot:A
boot
```

最后一步启动 `A:v1`，而持久状态中的 `confirmed=v2`、`fuse=v1`。根因是确认与熔丝递增不是同一个原子步骤；确认后、熔丝递增前断电，旧镜像仍通过熔丝检查。

## 命令

完整遍历并输出最短反例：

```sh
go run . check
```

独立重放最短反例：

```sh
go run . replay write:B:v2 set_boot:B boot confirm power_loss set_boot:A boot
```

查看持久、易失变量及断电复位值：

```sh
go run . vars
```

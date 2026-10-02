# Latch Crabbing / Coupling（Go）

这是一个只使用 Go 标准库实现的并发 B+ 树示例，用螃蟹脚加锁（Latch
Crabbing，也称 Lock Coupling）控制多线程树遍历。

## 协议规则

- 树使用一个永不分裂的固定头节点，头节点的唯一子节点是当前根节点。
- 读操作自上而下执行 `RLock` 耦合：先锁住子节点，再释放父节点。
- 写操作自上而下执行 `Lock` 耦合。
- 对插入而言，未满节点是安全节点；安全子节点已加锁后，其所有祖先都可以提前释放。
- 如果子节点已满，写线程保留祖先写锁，叶子分裂后沿已加锁路径向上安装分隔键。
- 锁的获取顺序永远是头节点到根、再到子节点，因此不会形成循环等待。

当前实现覆盖验收所需的并发读和并发插入；删除及借调/合并没有实现。

## 运行测试

```sh
go test ./...
go test -race ./...
```

本次运行结果：

```text
ok  	latchcrabbing	0.707s
?   	latchcrabbing/cmd/demo	[no test files]

ok  	latchcrabbing	2.204s
?   	latchcrabbing/cmd/demo	[no test files]
```

测试包括：

- 10 个读线程与 5 个插入写线程并发执行。
- 超时保护，验证螃蟹脚加锁无死锁。
- 并发插入后验证所有键可见且数量正确。
- 验证安全祖先确实被提前释放。
- 在无关叶子持有写锁时，验证读者仍能穿过高层节点访问其他叶子。

## 运行示例

```sh
go run ./cmd/demo
```

本次示例输出：

```text
readers=10 writers=5 preload=2000 inserts_per_writer=400
final_len=4000 height=8 read_errors=0
read_latches=173571 write_latches=32349 early_write_releases=20366
```

`early_write_releases` 表示插入线程在安全子节点加锁后提前释放的写锁数量；
该计数越高，高层节点被写线程独占的时间越短。

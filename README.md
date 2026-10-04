# merger — 可恢复的多路文件归并工具

把多个输入文件归并为一个按 `(key, source, sequence)` 稳定排序的输出文件，
支持内存限额、checkpoint 断点续跑，保证恢复后**不漏记、不重复输出**。

## 构建与使用

```sh
go build -o merger .

# 归并（可重复执行同一命令从中断处续跑）
./merger -mem 16MB -out out.txt src0.txt src1.txt src2.txt

# 校验：全量排序比对 + 每源 sequence 连续性核对
./merger -verify -out out.txt src0.txt src1.txt src2.txt
```

- 输入：每行一条记录，整行即 key；文件必须以换行结尾——**末尾不完整的行
  视为输入截断，直接丢弃**（merger 与 verifier 使用同一条规则）。
- 输出：每行 `key<TAB>source<TAB>sequence`，按该三元组全序排列
  （key 相同的记录按 source、再按 sequence 稳定排序；三元组唯一，故结果确定）。

## 设计

**两阶段外部归并**

1. **run 生成**：从 checkpoint 记录的 `(srcIdx, srcOffset, seq)` 处续读输入，
   缓冲记录直到内存限额，排序后落盘为一个 run 文件。限额按**实际缓冲字节**
   （key 长度 + 每条 16 字节开销）计算；单条大于限额的记录独占一个 run，
   即"记录可大于普通块"。
2. **k 路归并**：对每个 run 各缓冲一条记录做最小堆归并，输出按字节限额
   分块（`mem/2`）写入。

**崩溃一致性协议**

- run 文件先 `fsync`，再写 checkpoint；未入 checkpoint 的孤儿 run 文件在
  恢复时删除。
- 输出块先 `fsync`，再把 `(runOffsets[], outOffset)` 写入 checkpoint；
  checkpoint 本身用 tmp+rename+fsync 原子落盘。
- 恢复时输出文件被**截断到已确认偏移 `outOffset`**，未确认（ack 丢失）的
  尾部重写——因此既不漏也不重。
- checkpoint 只在 run 缓冲为空时推进输入位置，所以续读位置总是自洽的。

**故障注入（测试用）**

- `-crash-after N`：本次运行处理 N 条记录后模拟崩溃（run 阶段只在
  checkpoint 之后崩，merge 阶段可崩在未刷盘的输出块中间）。
- `-crash-before-checkpoint`：输出已 fsync 但 checkpoint 未写，模拟
  **输出确认丢失**；恢复时截断重传。
- 输入截断：直接把某个源文件截断到记录中间即可，工具丢弃尾部残记录。

**校验（`-verify`）**

1. 输出严格按 `(key, source, seq)` 递增；
2. 输出与所有源的**全量内存排序**逐条相等（不漏不重）；
3. 每个 source 的 sequence 恰好覆盖 `0..n-1`（源偏移一致）。

## 测试

```sh
go test ./...
```

覆盖：小内存多 run、超大记录、崩溃续跑（多个注入点）、输出确认丢失、
输入截断（含截断+崩溃组合）、完成后幂等重跑、源列表变更检测。

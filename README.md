# extsort — 可断点恢复的外部排序

两阶段外部排序：第一阶段把输入分块、内存稳定排序后落盘为有序运行段；
第二阶段做 k 路归并，并按检查点协议发布进度，崩溃后可从检查点恢复。

## 构建与运行

```sh
go build -o extsort .

# 排序（中断后重复执行同一命令即自动从检查点恢复）
./extsort sort -input in.txt -output out.bin -workdir work \
    -run-size 100000 -checkpoint-every 50000

# 模拟崩溃：发出 N 条记录后进程直接退出（不 flush、不关闭）
./extsort sort -input in.txt -output out.bin -workdir work -fail-after 1234

# 解码二进制输出为文本（seq \t key \t value）
./extsort dump -input out.bin

# 自检：逐故障切点注入崩溃并恢复，与一次内存稳定排序核对
./extsort selftest -records 20000 -run-size 1500 -checkpoint-every 1000
```

输入格式为每行 `key\tvalue`（无制表符则整行作为键）。

## 设计要点

- **稳定排序**：记录携带 uint64 全局输入序号，归并堆按 `(key, seq)` 比较，
  相同键严格保持原输入顺序。
- **运行段**：每段写完后 flush + fsync，并记录 SHA-256 摘要与长度（runs.go）。
- **检查点**（checkpoint.go）绑定：
  - 各输入段的路径 / 摘要 / 长度；
  - 各段游标（下一条未消费记录的字节偏移；堆中已预读未发布的记录不计入）；
  - 已发布输出前缀的长度与 SHA-256。
- **发布协议**（merge.go 的 publish）：先 flush + fsync 输出，
  再写临时检查点文件、fsync、原子 rename、fsync 目录。
  检查点引用的输出前缀因此一定已持久化。
- **恢复**（merge.go 的 newMerger）：
  - 逐段校验输入摘要，不符即拒绝；
  - 校验已发布输出前缀摘要，把输出截断到前缀长度——
    未同步缓冲的残骸一律丢弃，绝不信任；
  - 各段 seek 到游标，重建归并堆继续。
- **打开文件统计**：所有打开都经过 fdtrack.go 的包装，
  结束时报告峰值打开文件数（k 路归并时约等于 运行段数 + 2）。

## 自检

`selftest` 对每个故障切点（检查点边界前后、中点、首尾）：
从零开始归并并在切点处注入崩溃，随后反复从检查点恢复直到完成，
最终输出与一次内存稳定排序的结果做 SHA-256 比对，全部一致方通过。

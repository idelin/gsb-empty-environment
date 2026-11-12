# 内存映射截断信号隔离

安全解析服务映射不可信文件时，文件缩短可能让读取越过有效页。实现供C++服务调用的只读区间租约库，通过可注入页读取接口取得固定版本数据，不向解析器暴露裸映射指针；跨页失败返回精确已读前缀，取消与页缓存淘汰不得悬空引用。用截断和版本替换模型验证数据与引用生命周期。

## netsec 只读区间租约库

### 解决的问题

解析器直接持有 `mmap` 指针时，文件在解析过程中被截断或替换会让后续访问越过
有效页（SIGBUS / 读到陈旧数据）。本库把"映射"收敛到一个可注入接口之后，并以
版本化租约向解析器提供数据：

- 解析器只拿到 `netsec::Lease`，通过 `read()` 把字节**拷贝**进自己的缓冲区，
  从不接触裸映射指针。
- 每个租约绑定一个不可变版本（`VersionId`）。截断、替换都发布新版本，绝不
  修改旧版本已可达的数据。

### 核心抽象

- `IPageReader`：可注入的页读取接口（可包装 `pread`/`mmap`/网络存储/故障注入
  fake）。所有字节只经 `readPage(version, offset, dst, size)` 拷贝出来。
- `Volume`：服务入口，持有版本元数据、共享 LRU 页缓存和租约登记。
  - `publishVersion` / `truncateTo` / `replaceWith` 发布不可变版本；
  - `acquireLease(start, length)` 获取固定版本租约。
- `Lease`：只读、可移动不可复制的区间租约。
  - `read(offset, dst, len)` 逐页推进，跨页读取中途失败时，`ReadResult::bytes`
    是已拷贝的**精确前缀**，并以 `ReadOutcome` 区分 `Truncated` / `ReadError` /
    `Cancelled`；
  - `cancel()` 在页边界生效：在途读取完成当前页后停止并返回已读前缀，取消后的
    读取直接返回 `Cancelled` 且不触发任何后端 I/O。

### 生命周期与无悬空引用

- 每次成功取得的页是不可变 `PageBlock`，由 `shared_ptr` 在"共享缓存"和"租约
  pin 集合"之间共享所有权。
- LRU 淘汰只丢弃缓存的引用；租约已 pin 的页在租约释放前始终有效，因此缓存
  淘汰与正在进行的读取不会产生悬空引用。
- 版本替换后，旧 `VersionState`（含 reader 与长度）被存活租约持有；没有租约
  引用的旧版本在下次发布时从版本表剪枝回收。
- 租约析构/`release()` 后读取返回 `Expired`。
- I/O 在缓存锁之外调用，阻塞型 reader 不会卡住整个缓存；回填时处理并发竞争。

### 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug
cmake --build build
ctest --test-dir build --output-on-failure

# 内存与未定义行为检测
cmake -S . -B build-asan -DNETSEC_SANITIZE=ON
# 数据竞争检测
cmake -S . -B build-tsan -DNETSEC_TSAN=ON
```

测试（`tests/lease_tests.cpp`）用内存假文件建模截断与版本替换，覆盖：
跨页读取与截断精确前缀、版本替换下旧租约快照、缓存淘汰下的 pin 有效性、
在途/预取消语义、reader I/O 错误前缀、范围校验，以及并发发布+读取的引用生命周期。

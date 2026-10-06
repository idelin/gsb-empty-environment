# cachemodel

页缓存写回模型：按字节区间标脏，写回捕获页版本与脏集合快照，旧写回的
完成只清除它实际写成功的、且版本不新于快照的字节。

## 设计

- `dirtySet`（interval.go）：有序不重叠的半开区间集合，每个区间携带
  最后一次弄脏它的写版本。
- `Cache.Write`（cache.go）：拷贝数据、分配全局递增版本、按版本标脏。
- `writebackOnce`：持锁快照 `(version, dirtySet)` 并拷贝脏字节 → 标记
  `inflight` → 解锁写存储 → 完成时 `clearAcked(acked, version)`：
  仅清除被 ack 且脏版本 `<=` 快照版本的字节。因此：
  - 写回期间的新修改（版本更高）不会被旧写回完成清除；
  - 短写/出错只 ack 实际落盘的前缀，未写字节保持脏。
- `Flush`：循环写回直到干净，并等待在途写回。
- `Evict`：等待在途写回 → 若仍脏（期间被重新弄脏）再写回 → 干净后才
  从缓存移除。

## 逐字节版本核对（bytemodel_test.go）

oracle 跟踪每个字节的 `cacheVer`（最近一次缓存写版本）与 `storeVer`
（存储当前持有的版本），并校验：

1. `storeVer` 单调不减——过期写回永远不会覆盖更新的已落盘数据；
2. 存储收到的每个字节都等于某个真实写的载荷（版本归因取 `<= cacheVer`
   的最大匹配，同值写不会造成误判）；
3. 静止点（无在途写回、无并发写）脏集合恰好等于
   `{b : cacheVer[b] > storeVer[b]}`；
4. 驻留页数据等于 `cacheVer` 对应写的载荷；全量 Flush 后存储等于每个
   字节的最新写入值。

## 测试

- `TestWritebackDoesNotClearRedirtiedBytes`：写回在途时重新弄脏，旧写回
  完成只清未被动过的旧版本字节。
- `TestShortWriteClearsOnlyAckedBytes`：短写/注入错误只清 ack 前缀。
- `TestEvictWaitsForInflightWriteback`：淘汰阻塞直到在途写回完成，且
  期间重新弄脏的字节被再次写回后才移除页。
- `TestConcurrentWritesAndFlush`：并发写 + 后台 flush，对照 oracle。
- `TestRandomizedModelCheck`：随机写/flush/淘汰序列，每步核对不变式 3。

运行：`GO111MODULE=on go test -race ./...`

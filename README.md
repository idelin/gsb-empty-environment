# tile-block-store

按 tile 摘要共享不可变像素块的内容寻址存储：编辑写时复制、快照持有旧块、
持久 manifest、读租约、分代回收，回收前用独立存活根遍历复核。纯 TypeScript，
零依赖，仅用 Node.js 内置模块与内置测试运行器，不调用 Canvas 滤镜或任何图像处理库。

## 运行环境

- Node.js >= 22.6（推荐 v23.9.0，见 `.nvmrc`），利用原生 type stripping 直接运行 `.ts`

```sh
nvm use          # 可选，切到 v23.9.0
npm test         # node --test，11 个测试
npm run demo     # 运行演示样例（输出见下文）
```

## 设计要点

- **内容寻址块**：`BlockStore.intern()` 以自实现 FNV-1a 摘要（`src/hash.ts`）定位
  碰撞桶，桶内逐块做**完整像素比较**（`pixelsEqual`），同内容复用同一块；测试用
  `constantDigest` 强制全部碰撞验证桶内比较路径。
- **写时复制**：`TiledImage.setTile()` 先 intern 新块，再把"换根 + 引用账本增减"
  作为**同一决策**在 `rebind()` 中一次性提交并落盘；旧块由快照等存活根继续持有。
- **持久 manifest**：每次提交写入 `manifest.json.tmp`、fsync、原子 rename、再 fsync
  目录（`durability: "fsync"`，默认）。任何已提交状态自洽：崩溃只会留下孤儿块
  （之后被 GC 回收），绝不会让存活快照引用已删块。测试提供
  `durability: "atomic-rename"` 轻量档加速大规模交错枚举。
- **分代 GC**：`gc()` 推进 epoch，从所有存活根（图层 + 快照）**独立遍历**得到存活
  集合，不依赖引用计数；不可达块先标记（orphanMark），下一个周期才回收，给
  "已写入未提交"的崩溃窗口留出让期。`verify()` 用根遍历结果交叉核对账本。
- **读租约**：`lease()` 阻止回收被租约块，直到 `release()`；租约不计入账本。
- **验证图像**：`src/pixels.ts` 手工构造 RGBA 小图（空白/纯色/棋盘/渐变），
  `render()` 手工拼块，无任何图像库。

## 演示样例（`npm run demo` 实际输出）

```text
== 1. 两个空白图层共享同一空白块 ==
layerA tiles: blk0,blk0,blk0,blk0
layerB tiles: blk0,blk0,blk0,blk0
store stats: { blocks: 1, roots: 2, buckets: 1, epoch: 0, leases: 0 }

== 2. 写时复制：改 layerA 不影响 layerB ==
layerA(0,0) = blk1  layerB(0,0) = blk0
layerB(0,0) 仍为空白: true
layerA 其余 tile 仍与 layerB 共享: true

== 3. 快照持有旧块 ==
快照 s1 的 (0,0) 块: blk1 仍是棋盘块: true
layerA 当前 (0,0) 块: blk2

== 4. 读租约阻止回收 ==
gc#1 marked: [] reclaimed: []  租约块存活: true
gc#2 reclaimed: []  租约块存活: true
释放租约后 gc#3 marked: [ 'blk3' ]
gc#4 reclaimed: [ 'blk3' ]  块已回收: true

== 5. 删除快照只回收无引用块 ==
reclaimed: [ 'blk1' ] (棋盘块只被 s1 引用)
渐变块(被 layerA 引用)存活: true

== 6. 崩溃恢复：从 manifest 重开 ==
verify ok: true  live: 2  unreachable: 0
重开后 layerA(0,0) 与重开前一致: true
final stats: { blocks: 2, roots: 2, buckets: 2, epoch: 6, leases: 0 }
```

## 测试结果（`npm test`，Node v23.9.0）

```text
✔ dedup: identical content shares one block, blank tiles share across images
✔ collision: forced digest collision falls back to full pixel compare
✔ copy-on-write: two layers share a block, editing one leaves the other intact
✔ snapshot pins old blocks across later edits and GC
✔ deleteSnapshot reclaims only blocks with no remaining references
✔ generational GC: orphan survives its first cycle, reclaimed on the next
✔ read lease blocks reclamation until released
✔ ledger is cross-checked by independent root traversal, GC never trusts refcounts
✔ crash safety: every committed state on disk is self-consistent
✔ interleavings: exhaustive op sequences keep invariants and survive reopen
✔ interleavings: seeded random long sequences with reopen after every op
ℹ tests 11  ℹ pass 11  ℹ fail 0
```

交错验证覆盖：穷举 {块写入, 根发布/删除, 租约获取/释放, GC} 四操作全部
长度 ≤3 序列（84 条，强制摘要碰撞模式下），以及 12 条固定种子随机长序列
（每条 40 步），每步之后断言账本与独立根遍历一致、并重开 manifest 校验
存活根无悬空引用。

## 文件结构

- `src/hash.ts` — 摘要接口、FNV-1a 实现、强制碰撞用常量摘要
- `src/pixels.ts` — RGBA 像素工具与测试图样生成
- `src/store.ts` — 块存储：碰撞桶、引用账本、manifest 持久化、租约、分代 GC、复核
- `src/image.ts` — 写时复制图层、快照、手工渲染
- `src/demo.ts` — 演示样例
- `test/store.test.ts` — 全部测试

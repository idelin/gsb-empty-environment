# pixel-delta-history

局部像素编辑的差量历史系统：分支撤销、内存压缩、重叠编辑逆序恢复、
元数据（尺寸）变化纳入事务。纯 TypeScript，仅用 Node.js 内置模块
（`node:test` / `node:assert`），不依赖 Canvas 滤镜或任何图像处理库。

## 运行

需要 Node.js >= 23.6（内置 TypeScript 类型剥离，可直接执行 `.ts`）。

```sh
npm test    # node --test 'test/*.test.ts'
npm run demo
```

## 设计

- `src/rgba-image.ts` — 自建 RGBA 小图（`Uint8ClampedArray`，行主序）。
- `src/delta.ts` — 差量类型，均可 `apply`（重做）/ `revert`（撤销）：
  - `PixelDelta`：稀疏记录被触像素的索引与前态/后态字节，未变的像素不占空间；
  - `CropDelta`：尺寸元数据变化事务，记录前后宽高、偏移与被裁掉的
    上/下/左/右四条像素带，撤销时精确回填；
  - `CompositeDelta`：显式事务（`beginTransaction`/`commitTransaction`），
    多个操作作为一个历史节点原子撤销，逆序回滚。
- `src/editor.ts` — 历史树。提交即挂节点；撤销后新编辑自然分叉；
  `redo()` 沿最近使用的分支，`redo(i)` 显式选分支。
- `src/rle.ts` — 自建 PackBits 式 RLE。距当前位置超过 `compressAfter`
  步的差量在内存中压缩（取压缩/原始较小者），撤销/重做时按需解压。
- 历史淘汰：根到当前超过 `maxDepth` 且树干为线性时，最老差量被前向
  折叠进根快照。淘汰只改根快照与被弃节点，当前图像与保留差量不受影响。

## 验证（node:test，6 项全部通过）

- 两个重叠笔触：撤销后者，重叠点只恢复其前态（红），另一笔触不受影响；
  再撤销前者，全图回到透明；重做可完整重放。
- 裁剪后撤销：尺寸与全部被裁像素逐字节恢复；重做再次裁剪结果一致。
- 尺寸变化与笔触进入同一事务：一次撤销原子回滚两者。
- 分支撤销：撤销后新编辑分叉，`redo()` 跟随活动分支，`redo(0)` 切换分支。
- 历史淘汰：`maxDepth=3` 下提交 6 笔，当前图与独立计算的期望值逐字节
  一致；保留的 3 步可撤销，第 4 步返回 `false`，图像不损坏。
- RLE 往返一致；压缩后差量字节数显著下降，解压撤销结果正确。

## 运行样例（`npm run demo` 节选）

```
== two overlapping strokes ==  (12x7, depth 2, at "stroke-B (vertical)")
  ............
  .....B......
  .....B......
  .RRRRBRRRR..
  .....B......
  .....B......
  ............

== undo stroke-B: overlap (5,3) returns to red, A intact ==
  ............
  ............
  ............
  .RRRRRRRRR..
  ............

== crop to 8x5 ==  (8x5, depth 3, at "crop")
== undo crop: full 12x7 canvas restored ==  (12x7, depth 2)

  branches at stroke-A: stroke-B (vertical) | stroke-C (green box)

  [mem] after 10 dabs (compressed + evicted, depth capped at 4):
        nodes=5 deltaBytes=124 snapshotBytes=1024
  undos: true true true true false
```

## 测试结果

```
✔ overlapping strokes: undo restores each stroke pre-state in reverse order
✔ crop is a transaction: undo restores dimensions and cropped pixels
✔ metadata size change joins a multi-op transaction and undoes atomically
✔ branching undo: new edit after undo forks history, redo picks branches
✔ history eviction folds old deltas into the base without corrupting the image
✔ rle round-trip and in-memory compression keep undo correct
ℹ tests 6  ℹ pass 6  ℹ fail 0
```

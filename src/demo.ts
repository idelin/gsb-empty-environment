import { promises as fs } from "node:fs";
import path from "node:path";
import { BlockStore } from "./store.ts";
import { TiledImage, deleteSnapshot, snapshotTiles } from "./image.ts";
import { checkerPixels, gradientPixels, pixelsEqual } from "./pixels.ts";

const dir = path.resolve("demo-data");
await fs.rm(dir, { recursive: true, force: true });

const store = await BlockStore.open(dir);
const TILE = 8;

console.log("== 1. 两个空白图层共享同一空白块 ==");
const layerA = await TiledImage.createBlank(store, "layerA", 16, 16, TILE);
const layerB = await TiledImage.createBlank(store, "layerB", 16, 16, TILE);
console.log("layerA tiles:", layerA.tileIds().join(","));
console.log("layerB tiles:", layerB.tileIds().join(","));
console.log("store stats:", store.stats());

console.log("\n== 2. 写时复制：改 layerA 不影响 layerB ==");
const checker = checkerPixels(TILE, [255, 0, 0, 255], [0, 0, 255, 255]);
const checkerId = await layerA.setTile(0, 0, checker);
console.log("layerA(0,0) =", layerA.tileId(0, 0), " layerB(0,0) =", layerB.tileId(0, 0));
console.log("layerB(0,0) 仍为空白:", pixelsEqual(await layerB.getTile(0, 0), new Uint8Array(TILE * TILE * 4)));
console.log("layerA 其余 tile 仍与 layerB 共享:", layerA.tileId(1, 0) === layerB.tileId(1, 0));

console.log("\n== 3. 快照持有旧块 ==");
await layerA.snapshot("s1");
const grad = gradientPixels(TILE, 5);
await layerA.setTile(0, 0, grad);
const snapTile = snapshotTiles(store, "s1")[0];
console.log("快照 s1 的 (0,0) 块:", snapTile, "仍是棋盘块:", snapTile === checkerId);
console.log("layerA 当前 (0,0) 块:", layerA.tileId(0, 0));

console.log("\n== 4. 读租约阻止回收 ==");
const doomed = await store.intern(gradientPixels(TILE, 99));
const lease = store.lease(doomed);
let r = await store.gc();
console.log("gc#1 marked:", r.marked, "reclaimed:", r.reclaimed, " 租约块存活:", store.has(doomed));
r = await store.gc();
console.log("gc#2 reclaimed:", r.reclaimed, " 租约块存活:", store.has(doomed));
lease.release();
r = await store.gc();
console.log("释放租约后 gc#3 marked:", r.marked);
r = await store.gc();
console.log("gc#4 reclaimed:", r.reclaimed, " 块已回收:", !store.has(doomed));

console.log("\n== 5. 删除快照只回收无引用块 ==");
await deleteSnapshot(store, "s1");
await store.gc();
r = await store.gc();
console.log("reclaimed:", r.reclaimed, "(棋盘块只被 s1 引用)");
console.log("渐变块(被 layerA 引用)存活:", store.has(layerA.tileId(0, 0)));

console.log("\n== 6. 崩溃恢复：从 manifest 重开 ==");
const reopened = await BlockStore.open(dir);
const v = reopened.verify();
console.log("verify ok:", v.ok, " live:", v.liveBlocks, " unreachable:", v.unreachableBlocks);
const layerA2 = await TiledImage.attach(reopened, "layerA", 16, 16, TILE);
console.log("重开后 layerA(0,0) 与重开前一致:", layerA2.tileId(0, 0) === layerA.tileId(0, 0));
console.log("final stats:", reopened.stats());

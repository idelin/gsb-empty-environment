# chunkfilter

Go 图像库：按块接收整数像素，对图像执行 3x3 均值滤波，图像边界外复制最近边缘像素。

## 行为

- 图像按 `blockSize x blockSize` 的块交付，顺序任意；每行/列最后一个块可不足整块。
- 某块的滤波结果只在其自身及全部所需邻块（3x3 块邻域内存在的块）齐备后才发布。
- 每个块恰好发布一次，通过 `emit` 回调按可用顺序输出。
- 重复交付内容一致的块是空操作；内容不一致则报错。
- 坐标越界、块尺寸与网格位置不符、数据长度不符均报错。

## 用法

```go
f, _ := chunkfilter.New(width, height, blockSize, func(b chunkfilter.Block) {
    // b 为滤波后的块，Data 为行主序整数像素
})
for _, blk := range incomingBlocks { // 任意顺序
    if err := f.Add(blk); err != nil {
        // 处理重复不一致或非法块
    }
}
if f.Complete() { /* 全部块已发布 */ }
```

## 测试

```sh
go test ./...
```

测试以全图滤波结果为基准，覆盖乱序交付、角块优先、末块不足整块、单块/单像素图像、重复块（一致与不一致）及非法块。

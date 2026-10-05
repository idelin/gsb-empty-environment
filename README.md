# slottedpage

变长记录页存储库（Go）。每页固定大小，页头 + 槽目录从前向后增长，记录堆从页尾向前增长。

## 核心语义

- **句柄**：`Handle{PageID, Slot, Gen}`。压缩只移动记录物理位置，槽号与代际不变，有效句柄永远有效。
- **槽复用**：删除把槽置为墓碑并递增代际；插入优先复用墓碑槽，旧代际句柄全部失效（`ErrStaleHandle`）。
- **记录增长**：`Update` 原地释放旧空间（计入碎片），必要时先页内压缩再写入；空间不足返回 `ErrPageFull`，且失败前不做任何修改，整页字节不变。
- **空间统计**：`Stats()` 报告页头、目录、记录、连续空闲、碎片字节，恒满足 头+目录+记录+空闲+碎片 == 页大小。
- **序列化**：`Marshal`/`Unmarshal` 为独立编解码；解码端重新校验全部结构不变量（记录落在堆内、互不重叠、堆字节 == 记录 + 碎片）。

## 用法

```go
p, _ := slottedpage.NewPage(1, slottedpage.DefaultPageSize)
h, _ := p.Insert([]byte("hello"))
p.Update(h, []byte("hello, world")) // 增长
data, _ := p.Get(h)
p.Delete(h)                         // h 从此失效
p.Compact()                         // 合并碎片，句柄不变
img := p.Marshal()
q, _ := slottedpage.Unmarshal(img)  // 独立解码核对
```

## 测试

```sh
go test ./...
```

覆盖：碎片合并、空记录、最大页边界（精确填满 / 溢出失败且页面不变）、
槽复用代际失效、序列化前后独立解码一致性、随机操作下的空间不变量。

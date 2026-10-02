# jsonproj — 结构化 JSON 扁平化列式投影器

将半结构化 JSON 动态拆解为按字段路径组织的虚拟列。查询只指定少数叶子路径时，
投影器基于 `encoding/json` 的流式 token Decoder 单次扫描文档，仅物化相关叶子值；
无关子树（如订单商品列表）在 token 层直接跳过，**不做反序列化**。

## 能力

- `InferSchema(r)`：单次流式扫描，自动推断全部叶子路径（数组元素带下标，
  如 `items[0].sku`；空对象/空数组视为叶子）。
- `NewProjector(paths...).Project(r)`：按指定叶子路径建立独立列存切片
  （`map[path]*Column`），缺失路径返回空列。

## 运行

```sh
go run ./cmd/demo   # 运行样例：推断 schema + 投影 user.address.city
go test ./...       # 测试（含与商品列表无关的城市维度提取验证）
go test -bench=. .  # 基准：仅投影城市列
```

## 验收点

输入复杂嵌套订单 JSON（用户/地址/商品列表/支付/物流），投影器按
`user.address.city` 建立独立列存切片；提取城市维度时，`items`、`payment`、
`logistics` 等无关结构只被跳过（`skipValue` 消费 token），从不进入 Go 值。

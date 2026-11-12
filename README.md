# OPC关系路径与OLE包名

办公文档导入服务读取 OPC 包中的关系文件，攻击者可让 Target 使用 ../、反斜线、百分号编码点段，或让嵌入 OLE 对象携带带路径的原始文件名。写供 Go 导入服务调用的包校验组件，按 OPC 部件名规则把关系目标解析到包内，外部目标只在调用方允许时保留，且 oleObject 包名只能取最后一段安全文件名。用越出包根、编码点段、重复部件名和合法嵌入图片验证。

## 组件

- `Package.AddPart` / `NewPackage`：按 ECMA-376 Part 2（OPC）部件名规则
  校验并登记部件，解码后忽略大小写检测重复部件名。
- `Package.ResolveTarget`：把 `.rels` 中的 Target 解析为包内部件名。内部目标
  拒绝反斜线，先百分号解码再按 RFC 3986 合并并移除点段，越出包根返回
  `ErrPathEscape`；外部绝对 URI 仅在 `allowExternal` 为 true 时原样保留。
- `SafeOLEPackageName`：从嵌入 OLE 对象的原始文件名中剥离目录、盘符、UNC
  前缀与多层百分号编码，只返回最后一段安全文件名，无法清洗时返回
  `ErrUnsafeOLEName`，由调用方改用受控占位名。

## 验证

```sh
go test ./... -race -cover
```

测试覆盖越出包根的 `../`、百分号编码点段（`%2e%2e`、`%2f`）、大小写与
编码变体的重复部件名，以及一条合法嵌入图片与 OLE 对象的完整导入链路。

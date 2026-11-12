# DHCP选项串接安全

接入网关读取重复DHCP选项时，检查层与业务层必须使用相同拼接结果。给Go解析宿主实现选项归一包，按主选项区、file区、sname区的明确顺序处理overload和拼接，PAD与END各自生效，固定长度选项拼接后再校验。用恶意跨区切片与重复overload测试，失败不返回部分绑定信息。

## 包结构（`package netsec`）

- `dhcp.go`：BOOTP 固定布局常量、选项码、哨兵错误、`Options` 与 `Binding` 类型。
- `parse.go`：`ParseOptions` 按 **主选项区 → file 区 → sname 区** 的固定顺序扫描；
  重复选项按 RFC 3396 顺序拼接，存储值全部拷贝，不引用入组报文内存。
- `validate.go`：`Options.Validate` 在拼接完成后做固定长度（如子网掩码/ServerID/租期 4 字节、
  MessageType 1 字节）与地址列表 4 字节对齐校验。
- `options.go`：`Has`/`Raw`/`Byte`/`IPv4List` 访问器，`Raw` 返回拷贝。
- `binding.go`：`ParseBinding` 复用 `ParseOptions` 的同一份归一结果再校验、再组装绑定；
  任何一步失败均返回 `nil` 绑定与错误，绝不返回部分绑定信息。

## 关键语义

- **overload（选项 52）**：只认主选项区中的唯一一次出现；重复（即使值相同）、长度非法、
  值越界均返回 `ErrOverloadValue`；file/sname 区内出现 52 返回 `ErrNestedOverload`。
  bit0 解析 file 区（128 字节），bit1 解析 sname 区（64 字节），未置位的区保持 BOOTP 旧语义不扫描。
- **PAD(0)**：只跳过当前区的一个字节，不终止扫描。
- **END(255)**：只终止所在区；区字节耗尽但无 END 视为隐式结束。
- **边界**：每个区按自身起止切片扫描，长度字节不可能把读取拖到另一个区或报文尾部之外，
  越界返回 `ErrTruncated`。
- **校验时机**：固定长度检查针对跨区拼接后的完整值；检查层（`Validate`）与业务层
  （`ParseBinding`）消费同一个 `Options` 实例，结论不可能分叉。

## 测试

- `dhcp_test.go`：重复选项拼接、PAD/END 作用域、overload 顺序与位选择、重复/嵌套 overload、
  恶意跨区切片（主区缺长度字节、file 区长度越界到 sname、sname 区长度越界）、
  拼接后固定长度校验、归一结果与报文内存无别名。
- `binding_test.go`：绑定正常路径、各类失败一律返回 nil、hlen 非法拒绝、
  检查层与业务层使用同一归一结果（事后篡改报文不影响绑定视图）。

```sh
go vet ./...
go test -race -count=1 ./...
```

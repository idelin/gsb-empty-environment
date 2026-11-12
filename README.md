# IPv6邻居发现选项一致性

邻居发现接收器不能因选项长度为零或重复链路地址进入死循环或选错身份。写供Go网络宿主调用的解析准入包，校验跳限、消息类型约束、选项长度与源地址关系，重复安全关键选项拒绝；未知选项按长度跳过但共享总预算。用构造报文验证截断、零长度、重复地址和伪造路由通告。

## ndadmit 包

`ndadmit` 是 ICMPv6 邻居发现（RFC 4861，含 RFC 4191/8106 约束）的解析与
准入包。宿主在更新任何邻居缓存或路由状态前，先调用 `ndadmit.Parse`（或带
`Config` 的 `ParseWith`）这一道关卡：

- 跳限必须为 255，ICMPv6 code 必须为 0，固定头与选项不得截断；
- 选项长度为 0 立即返回 `ErrZeroOption`，解析游标每次至少前进 8 字节，杜绝
  零步长死循环；
- SLLA、TLLA、MTU、Redirected Header 等身份相关单例选项重复出现时返回
  `ErrDuplicateOption`，Prefix/Route 等天然可重复的选项保留；
- 未知选项只按长度字段跳过，但与已知选项一样计入 `OptionCount` 共享预算，
  长度越界仍按 `ErrTruncated` 拒绝；
- 校验源地址关系：DAD 的未指定源不得携带 SLLA、RA/Redirect 必须来自
  link-local、组播 NA 不得携带 TLLA、Solicited 标志与未指定源互斥；
- `Config{VerifyChecksum: true}` 可启用 ICMPv6 伪首部校验和验证。

所有拒绝原因都包装为可 `errors.Is` 分类的哨兵错误（`ErrHopLimit`、
`ErrTruncated`、`ErrZeroOption`、`ErrDuplicateOption`、`ErrBadFields` 等）。

运行测试：

```sh
go test ./...
go test ./ndadmit/ -race
go test ./ndadmit/ -run '^$' -fuzz FuzzParse -fuzztime 10s
```

# mailauth — 字节级 DKIM / ARC 验签与反伪造分析包

面向 Go 邮件安全宿主的可导入包，对原始 RFC 5322 报文执行 RFC 6376 DKIM
与 RFC 8617 ARC 认证链验签，并给出可机读的反伪造裁决。

- 完全离线：不发起任何网络请求，不依赖任何第三方 DKIM 库。
- 密码学仅使用标准库 `crypto/rsa`、`crypto/ed25519`、`crypto/sha256`。
- 公钥由宿主通过 `KeyResolver` 注入（提供本地 `MemoryKeyStore`）。

## 文件结构

| 文件 | 职责 |
| --- | --- |
| `message.go` | RFC 5322 字节解析、CRLF 归一化、有序头块、ARC 作用域 |
| `canonical.go` | simple/relaxed 头与正文规范化、正文哈希、空正文处理 |
| `tags.go` | DKIM/AMS/AS 标签解析（`h=`、`bh=`、`b=`、`l=`、`t=`、`x=`、`i=`、`cv=`） |
| `signeddata.go` | RFC 6376 §5.4 自底向上消费 `h=`、过签空占位、`b=` 置空 |
| `dkim.go` | DKIM 验签主流程、正文长度注入、时间校验、RSA/Ed25519 验签、策略裁决 |
| `arc.go` | ARC 实例连续性、cv 状态机、AMS/AS 分跳作用域验签 |
| `verdict.go` | 裁决模型、Finding 代码、策略、密钥解析接口 |

## 强制安全规则

- 多个 `From:` 头一律拒收（Multiple-From Spoofing）。
- `h=` 未覆盖 `From` 的签名拒收。
- 过签严格按 RFC 6376 §5.4.2 处理：`h=` 中超出实际头数的槽位以空字节占位参与
  签名，签名后再注入的 `From` 无法占用任何已签名槽位。
- 带 `l=` 且规范化正文长于限制时标记 `body_length_truncation_injection`
  （高危），默认策略拒收；`l=` 大于正文长度按 permerror 处理。
- `x=` 必须晚于当前时间；`x=` 与 `t=` 同时存在时必须 `x > t`。
- ARC 实例必须为连续 `1..N`，禁止跳号、重复，最多 50 跳；`i=1` 必须
  `cv=none`；后续出现 `cv=fail` 整链立即作废；`cv=pass` 与实际验签不符判伪造。

## 测试

测试使用真实生成的 RSA-2048 与 Ed25519 密钥构造签名与攻击样本：

- 合法 RSA / Ed25519 签名、simple/simple 与 relaxed/relaxed 往返。
- RFC 规范化向量（折行展开、WSP 折叠、尾部空行剥离、空正文哈希）。
- 多重 `From`、`h=` 不含 From、过签后注入 From、正文篡改。
- `l=` 截断注入：短前缀签名、精确边界后追加钓鱼内容。
- 过期签名、`x<=t` 非法时间关系、裸 LF 归一化。
- ARC 合法链、跳号、重复实例、首跳 cv 非法、cv=fail 作废、AMS/AS 篡改、
   集合缺失、50/51 跳边界。

运行：

```sh
go test ./... -race -cover
```

# 兑换绑回跳地址

用另一个redirect_uri兑换授权码，授权码会发到攻击者的回跳。兑换时的redirect_uri必须与授权请求的字节级相同，包括查询顺序；用户信息或片段出现则拒绝。回跳地址完全相同的兑换成功。由授权服务调用可导入的Go库。调换查询参数顺序并给地址加上用户信息。

## 用法

授权服务在授权请求中必须原样保存 `redirect_uri` 字符串，在兑换授权码时调用
`ValidateRedirectURI`：

```go
if err := netsec.ValidateRedirectURI(storedRedirectURI, tokenRequestRedirectURI); err != nil {
	// 拒绝兑换，不要签发授权码或令牌
}
```

校验规则：

- `redirect_uri` 必须与授权请求中的值逐字节相同，不做 URL 规范化、解码或重新编码。
- 查询参数的顺序、重复参数和百分号转义都属于比较内容，任何差异都会被拒绝。
- 任一地址包含 userinfo（例如 `https://trusted.example@attacker.example/cb`）时拒绝。
- 任一地址包含片段（包括空的尾部 `#`）时拒绝。
- 只有绝对 URI 才会被接受；`http`/`https` URI 还必须包含主机名。

可以使用 `errors.Is` 区分错误：

```go
switch {
case errors.Is(err, netsec.ErrRedirectURIMismatch):
	// redirect_uri 不一致
case errors.Is(err, netsec.ErrRedirectURIUserInfo):
	// 地址包含 userinfo
case errors.Is(err, netsec.ErrRedirectURIFragment):
	// 地址包含片段
case errors.Is(err, netsec.ErrInvalidRedirectURI):
	// 地址格式无效
}
```

测试覆盖了完全相同的回跳地址、调换查询参数顺序、修改查询内容、添加
userinfo 和添加片段等场景。

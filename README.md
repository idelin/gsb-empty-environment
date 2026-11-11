# ldapfilter

目录查询把用户输入拼进LDAP过滤器，括号和星号会改写查询逻辑。给Go身份服务实现过滤器构造与解析包：按RFC 4515转义值；解析器支持与、或、非、存在、子串、近似和扩展匹配，拒绝空集合、未闭合括号及非法转义；构造出的过滤器解析回来必须得到同一棵语法树。用随机生成的语法树做往返测试，并验证注入样本只被当作字面值。

## 用法

构造过滤器时，所有用户输入都通过 RFC 4515 转义写入，`*`、`(`、`)`、
`\` 和 NUL 字节编码为 `\XX`，无法再改变过滤器结构：

```go
filter, _ := ldapfilter.NewEquality("cn", userInput)
rendered, _ := ldapfilter.Build(filter) // 例如 (cn=admin\29\28|\28objectClass=\2a)
```

解析时转义序列被还原为字面值，输出与构造时相同的语法树：

```go
parsed, err := ldapfilter.Parse(rendered)
// parsed.(*ldapfilter.EqualityFilter).Value == userInput
```

支持的节点：`AndFilter`、`OrFilter`、`NotFilter`、`EqualityFilter`、
`SubstringsFilter`、`PresentFilter`、`ApproxFilter`、`GreaterOrEqualFilter`、
`LessOrEqualFilter`、`ExtensibleFilter`。

## 安全与校验

- `EscapeValue` 实现 RFC 4515 第 3 节的值转义。
- 解析器拒绝空 `&`/`|` 集合、未闭合括号、尾随字符、非法转义（`\` 后非两位
  十六进制）、未转义 NUL、非子串断言中的裸 `*`，以及缺少属性/匹配规则的
  扩展匹配。
- `Build` 对手工构造的畸形树返回错误；`String()` 在畸形节点上 panic，
  正常代码路径应使用构造器与 `Build`。

## 测试

```sh
go test ./... -race -cover
```

测试覆盖固定用例、注入样本（等值/子串/扩展三种断言）、以及使用
`math/rand/v2` 随机生成的深度受限语法树往返（`Build` → `Parse` →
`reflect.DeepEqual`）。

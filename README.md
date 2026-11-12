# 采集学术页面的JSON-LD时，同一作者和文章可能分散在多个脚本块中，直接取第一个对象会漏掉引用关系

采集学术页面的JSON-LD时，同一作者和文章可能分散在多个脚本块中，直接取第一个对象会漏掉引用关系。实现供Go网页抽取宿主调用的元数据图包，输入脚本JSON树、页面绝对URL及允许的Article和Person类型IRI；支持局部@context的术语映射、前缀、@vocab与@base，嵌套作用域退出后恢复，远程context返回未解析而不联网。先展开所有属性和相对@id，再按IRI合并节点；@list保持顺序，普通多值按展开值去重，单值属性集合由调用方明确给定，同一单值属性冲突保留双方来源且不输出确定值。沿author、name及headline抽取文章与作者，引用环输出未解析边，不能无限展开。以等价前缀重命名、脚本顺序排列和嵌套context反例验证抽取图及原节点来源一致。

## 使用方式

```go
graph, err := taskpkg.Build(
    scripts,                 // []json.RawMessage 或已解析的 JSON 树切片
    "https://example.org/article/1",
    []string{"https://schema.org/Article"},
    []string{"https://schema.org/Person"},
    []string{"name", "headline"}, // 调用方声明的单值属性
)
```

也可以使用 `taskpkg.Options`：

```go
graph, err := taskpkg.Build(taskpkg.Options{
    Scripts:      scripts,
    PageURL:      pageURL,
    ArticleTypes: articleTypes,
    PersonTypes:  personTypes,
    SingleValued: singleValuedProperties,
})
```

`Graph.Nodes` 是展开后的 IRI 节点图，`Node.Properties` 中的每个值都带有
`Source`（脚本序号和规范化 JSON Pointer）。`@list` 的值保留内部顺序；普通
多值按展开值去重并合并来源。`Graph.Articles` / `Graph.Authors` 是沿
`author`、`name`、`headline` 得到的抽取视图。远程 `@context` 不会被请求，
而是记录在 `Graph.UnresolvedContexts`；悬空引用、类型不符和引用环记录在
`Graph.UnresolvedEdges`，遍历同时带深度上限。`Graph.Scripts` 提供脚本序号
到内容哈希的映射，脚本顺序变化时可据此归一化来源而无需依赖序号。

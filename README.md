ORM 和视图嵌套常生成多余的自连接或主外键连接，但盲目消除连接在有可空外键、非唯一键或 SQL 多重集（BAG，未去重）语义下会改变输出行数或漏掉滤除效果。为 SQL 逻辑重写器交付可导入的 Go 连接消除包。输入查询结构（多表内连接与左外连接、是否带 DISTINCT、输出投影列、等值连接条件与 `IS NOT NULL` 等单表过滤谓词）以及模式约束（每表主键、唯一键、每列是否可空、外键引用约束与函数依赖集）。核心判定：在左外连接 `L LEFT JOIN R ON cond` 中，若 R 的连接列覆盖其唯一键（使 L 每行至多匹配 R 一行）且 R 的列既不出现在输出投影也不被上层非空谓词引用，无论集合还是多重集语义均可安全消除 R；在内连接 `L INNER JOIN R ON fk = pk` 中，仅当存在声明外键指向 R 主键、L 的外键列非空（或查询已含 `fk IS NOT NULL`）且 R 非连接列未被引用（或可通过等价列替换到 L）时才可消除 R；对于自连接 `T a JOIN T b ON a.k = b.k`，仅当 k 覆盖唯一键且全非空时可折叠为单表并合并两别名的过滤谓词。组件迭代执行全部合法消除，输出最终最小保留表集、列重映射与重写后谓词。验收在有限域上生成全部小表实例按精确多重集语义逐行比对消除前后输出完全一致（含重复行重数），覆盖可空外键导致内连接不可消除、加了 `fk IS NOT NULL` 后变可消除、无唯一键左连接在 BAG 语义下因倍增行数不可消除但在 DISTINCT 下可消除，以及链式三表外键级联消除。

## 实现

零第三方依赖。推荐导入实现子包；模块根包也通过类型别名/转发暴露同一套 API：

```go
import js "prescreen.local/ps-0f39c054e3/joinselim"
// 或导入模块根：js "prescreen.local/ps-0f39c054e3"

schema := js.NewSchema().
    AddTable(js.Table{
        Name:       "parent",
        Columns:    []js.Column{{Name: "id", Nullable: false}},
        PrimaryKey: []string{"id"},
    }).
    AddTable(js.Table{
        Name: "child",
        Columns: []js.Column{
            {Name: "id", Nullable: false},
            {Name: "parent_id", Nullable: true},
        },
        PrimaryKey: []string{"id"},
    }).
    AddForeignKey(js.ForeignKey{
        Table: "child", Columns: []string{"parent_id"},
        ReferencedTable: "parent", ReferencedColumns: []string{"id"},
    })

query := js.Query{
    From: js.TableRef{Alias: "c", Table: "child"},
    Joins: []js.Join{{
        Type:  js.InnerJoin,
        Alias: "p",
        Table: "parent",
        On:    []js.Equality{{Left: js.Col("c", "parent_id"), Right: js.Col("p", "id")}},
    }},
    Projection: []js.Ref{js.Col("c", "id"), js.Col("p", "id")},
    Where:      []js.Predicate{js.NotNull(js.Col("c", "parent_id"))},
}

result, err := js.Rewrite(query, schema)
// result.RetainedAliases == ["c"]
// result.ColumnMap["p.id"] == "c.parent_id"
// result.Predicates retains c.parent_id IS NOT NULL
```

## 支持的消除规则

- 左外连接：右表连接列覆盖主键/唯一键，且右表别名不出现在投影、`WHERE` 或后续连接条件中时，在 BAG 与 DISTINCT 下均可消除。
- DISTINCT 左外连接：即使连接列不唯一，只要右表完全不被外部引用，重复匹配产生的相同输出行也会被 DISTINCT 收敛，因此可消除；BAG 下保守保留。
- 内连接外键：必须存在指向父表唯一键/主键的声明外键，组合外键的每一列都由模式标记为 `NOT NULL` 或被保留的查询谓词证明非空；父表键列可重映射到子表外键列，其他父表列被引用时阻止消除。
- 自连接：同一基表的两个别名通过同一物理唯一键列等值连接，并且键列由模式或保留谓词证明非空时，折叠到一个别名；两个别名的附加 `ON`/`WHERE` 谓词会重映射并合并。
- 重写器按连接逆序迭代到不动点，因此 `a -> b -> c` 外键链会先消除 `c`，再消除 `b`。

## 多重集验收

包内附带内存求值器 `joinselim.Evaluate` 和精确多重集比较 `joinselim.EqualMultiset`。测试在有限域中枚举所有小表实例并逐行比较重写前后结果：

```sh
go test ./... -count=1
```

覆盖包括：

- 可空外键在没有 `fk IS NOT NULL` 时保留，盲目删除会改变空外键行。
- 增加 `fk IS NOT NULL` 后消除父表，并保留该过滤谓词和 `parent.id -> child.fk` 映射。
- 无唯一键左连接在 BAG 下保留；重复右表行确实倍增输出，在 DISTINCT 下消除并通过全部实例比对。
- 唯一键左外连接、自连接谓词合并、可空唯一自键的非空证明。
- 90 个链式三表合法外键小实例上级联消除 `c`、`b`，仅保留 `a`。

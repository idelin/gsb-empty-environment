# verenum

多版本枚举库（Go，仅标准库）：数值 ID 是稳定的序列化身份，名称随版本变化。

## 语义保证

- **稳定 ID**：重命名只改当前规范名，不改变序列化身份；规范输出始终用目标版本的当前名。
- **旧名别名**：旧名称以别名形式继续可解码；同版本内同一名字绑定到两个不同 ID 在注册时拒绝。
- **未知值透传**：未知字符串按原词法形式原样往返；未知数值保留原数字，绝不强转成首项。
- **多版本合并**：跨版本改名合法（自动合成旧名→新名的边）；禁止把两个不同稳定 ID 映射成同一个活跃名字（`MergeError`）；别名跨版本指向不同 ID 允许合并，但迁移返回 `AmbiguityError`，不擅自择一。
- **最短冲突链**：对别名图做 BFS，给出每个候选 ID 的最短链，并带调用方传入的原始字段路径（如 `items[2].status`）。
- **全路径核验**：`VerifyPath` 校验小版本序列上每个名字始终表示同一 ID，首个分歧即报错。
- **历史快照**：`Snapshot` 深拷贝冻结当时的 schema，之后的别名撤回不影响历史文档解码。

## 序列化范围

- 每个枚举字段接受**一个 JSON 标量**：字符串（名字/别名）或整数（稳定 ID）。
- 不支持数组/对象/浮点；解码只用 `encoding/json` + `strconv`，不执行任何宿主代码。
- 安全约束：先校验原始字节长度（`MaxInputLen=1MiB`、`MaxNameLen=256`）再解码/分配，超大输入直接拒绝。

## API 速览

```go
r := verenum.New()
r.Register(1, 1, "red")                       // v1: id=1 "red"
r.Register(2, 1, "crimson", "red")            // v2: 改名，"red" 保留为别名
name, err := r.Migrate(1, 2, "red", "payload.color") // -> "crimson"
v, _ := r.Decode(2, []byte(`"red"`))          // 已知：{ID:1, Known:true}
b, _ := r.Encode(2, v)                        // "crimson"
snap, _ := r.Snapshot(2)                      // 冻结历史 schema
r.VerifyPath([]verenum.Version{1, 2}, "color") // 迁移一致性核验
```

## 运行

```bash
go test ./...          # 单元测试
go run ./cmd/example   # 运行样例
```

## 样例输出

```
$(cat /private/tmp/run-out.txt)
```

## 测试结果

```
$(cat /private/tmp/test-out.txt)
```

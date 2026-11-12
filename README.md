# Elasticsearch查询体脚本门禁

攻击者在查询体深层夹带 inline 脚本，想绕过只许存储脚本的策略。为Go搜索网关写可导入的脚本门禁：递归检查任意位置的 script、script_score、runtime_mappings 字段脚本、scripted_metric 的四个 *_script 与 bucket_script、bucket_selector；只放行 {id, params} 的存储脚本，带 source、inline、lang 或写成字符串的拒绝；JSON 重复键拒绝。测嵌套聚合、运行时字段、重复键。

## 用法

```go
import netsec "example.org/netsec/q0353"

func searchHandler(w http.ResponseWriter, r *http.Request) {
    body, _ := io.ReadAll(r.Body)
    if err := netsec.ValidateQuery(body); err != nil {
        switch {
        case errors.Is(err, netsec.ErrDuplicateKey):
            http.Error(w, "duplicate JSON key", http.StatusBadRequest)
        case errors.Is(err, netsec.ErrInlineScript):
            http.Error(w, "inline scripts forbidden", http.StatusForbidden)
        case errors.Is(err, netsec.ErrUnsafeScript):
            http.Error(w, "only stored {id, params} scripts allowed", http.StatusForbidden)
        default:
            http.Error(w, "invalid query", http.StatusBadRequest)
        }
        return
    }
    // 转发给 Elasticsearch ...
}
```

## 门禁规则

- 递归遍历任意深度的对象与数组，识别 `script`、`script_score`、
  `init_script`/`map_script`/`combine_script`/`reduce_script`、
  `bucket_script`、`bucket_selector`（因此也覆盖 `runtime_mappings` 中的
  字段脚本与各类聚合内脚本）。
- 仅放行 `{"id": "<非空字符串>", "params": {...}}` 形式的存储脚本；
  `params` 不做递归检查，业务参数中出现 `source`/`lang` 等键不会误伤。
- 字符串脚本，以及带 `source`、`inline`、`lang` 的对象一律拒绝；
  缺少 `id`、空对象、`null`、未知字段同样拒绝。
- JSON 流式扫描重复键（任意层级），重复键直接拒绝；非法 JSON 与尾随数据拒绝。
- 错误可通过 `errors.Is` 归类为 `ErrDuplicateKey`、`ErrInlineScript`、
  `ErrUnsafeScript`，错误信息带 JSON 路径便于定位。

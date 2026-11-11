# safelabel

文件审批页面以可视名称展示附件，双向覆盖符可能隐藏真实扩展名。写可由Go模板直接调用的文件名渲染包，将控制符呈为不可混淆的可见记号，隔离相邻文本方向，保留原始字节范围用于高亮，严禁注入HTML。测试组合字素、非法UTF-8、混合数字及恶意标签经模板渲染后的结果。

## 安全目标

- **控制符可见化**：C0/C1 控制符、全部双向嵌入/覆盖/隔离控制符（含
  U+202E RLO）以及零宽连接符等不可见格式符，统一渲染为
  `⟨RLO⟩`、`⟨TAB⟩` 形式的等宽记号，并带 `title` 全称提示；
  非法 UTF-8 字节逐字节渲染为 `⟨BAD-UTF8:0xFF⟩`。
- **方向隔离**：输出整体包裹在
  `<span dir="auto" style="unicode-bidi:isolate">` 中，
  文件名内部的 RTL/LTR 方向不会影响相邻页面文本。
- **字节范围可追溯**：每个片段返回 `Start/End` 字节偏移（左闭右开），
  记号与 `<mark>` 高亮均带 `data-b0/data-b1`，与原始字符串严格对齐。
- **杜绝 HTML 注入**：所有输入文本经 `html.EscapeString` 转义，
  输出中的标签只来自本包固定模板；返回 `template.HTML`，
  在 `html/template` 中不会被二次转义。

## API

- `Render(name string) template.HTML`：渲染带方向隔离的安全片段。
- `RenderHighlight(name string, ranges []Range) template.HTML`：
  按原始字节区间高亮，自动过滤越界区间、合并重叠区间、对齐 rune 边界。
- `Segments(name string) []Segment`：返回带字节范围与类型的片段，
  供程序化高亮或审计使用。
- `FuncMap() template.FuncMap`：注册 `safelabel` 与 `safelabel_hl`
  两个模板函数。

## 模板用法

```go
tmpl := template.Must(template.New("").
    Funcs(safelabel.FuncMap()).
    Parse(`<li>{{ safelabel .Name }}</li>`))

tmpl.Execute(w, map[string]any{
    "Name": "budget_2026\u202etxt.exe",
})
// <li><span class="safelabel" dir="auto" ...>budget_2026<span ...>⟨RLO⟩</span>txt.exe</span></li>
```

高亮（区间为原始文件名的字节偏移）：

```go
out := safelabel.RenderHighlight("report.txt", []safelabel.Range{{0, 6}})
```

## 测试

- `go test ./...`：组合字素、非法 UTF-8、混合数字、RLO 伪装扩展名、
  恶意标签经 `html/template` 渲染的结果，以及字节范围不变量。
- `go test -fuzz FuzzRenderSafety -fuzztime 30s ./...`：随机字节输入下
  校验输出恒为合法 UTF-8、控制符不泄漏、标签仅来自本包。

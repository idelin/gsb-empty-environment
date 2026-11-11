# rpalocate

RPA流程在网页改版后点错按钮，因为定位器只记录了一条XPath。给Go RPA运行时实现元素定位包：录制时保存文本、属性、角色和相对邻居等多个锚点，执行时在DOM快照上为候选元素打分；最高分不唯一或低于阈值时报告歧义而不点击，匹配理由可解释。用改版前后的页面快照测试正确定位、拒绝和误点情况。

## 用法

```go
// 录制：从旧页面快照中的目标元素提取多锚点定位器
root, _ := rpalocate.ParseHTML(oldPageHTML)
target, _ := rpalocate.FindByXPath(root, "//button[2]")
loc := rpalocate.Record(target) // 保存文本、属性、角色、相对邻居锚点

// 执行：在改版后的 DOM 快照上为所有可交互候选打分
newRoot, _ := rpalocate.ParseHTML(newPageHTML)
match, err := rpalocate.Locate(newRoot, loc)
var amb *rpalocate.AmbiguityError
var low *rpalocate.LowConfidenceError
switch {
case errors.As(err, &amb):
	// 最高分不唯一：拒绝点击，amb.Candidates 含各候选得分与理由
case errors.As(err, &low):
	// 最高分低于阈值：拒绝点击，low.Best 含最佳候选的理由
case err == nil:
	click(match.Element) // match.Score / match.Reasons 可解释
}
```

## 设计要点

- **锚点**：文本、稳定属性（id/name/type/class/aria-label 等）、ARIA 角色（隐式推断，故 `<button>` 录制可匹配 `<a role="button">`）、相对邻居（前一兄弟文本、所在区块标题）。
- **打分**：默认权重文本 0.35、属性 0.25、邻居 0.25、角色 0.10、标签 0.05；标签权重最低，因为改版常换标签。可用 `WithThreshold` / `WithWeights` 调整。
- **拒绝点击**：最高分低于阈值（默认 0.6）返回 `LowConfidenceError`；最高分并列返回 `AmbiguityError`。
- **测试**：`testdata/page_before.html` 与 `page_after.html` 模拟改版，覆盖同页回放、改版后正确定位、旧 XPath 失效/误点、歧义拒绝、低置信度拒绝。

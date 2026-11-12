# 爬虫抓到的列表中相邻页面可能共享置顶记录，不能把重复边界当作翻页停止

爬虫抓到的列表中相邻页面可能共享置顶记录，不能把重复边界当作翻页停止。为Go采集宿主实现分页进展判定包，输入每页有序业务键、置顶标记与显式下一页游标；进展只按非置顶新键或新游标推进，重复页面形成游标环时输出完整循环证据，空页但有新游标可继续。预算耗尽为未完成，只有明确无下一页才完成。用动态插入、全置顶页与游标循环模型核对采集集合，不自行推断上游完整性。

## 用法

```go
import taskpkg "example.org/domainreissue/q074"

tracker := taskpkg.NewTrackerAt(20, "start")

for {
    page := fetch(tracker.CurrentCursor())
    result := tracker.Observe(taskpkg.Page{
        Keys:       page.Keys,
        Pinned:     page.Pinned,
        NextCursor: page.NextCursor,
        HasNext:    page.HasNext,
    })

    switch result.Status {
    case taskpkg.StatusContinue:
        continue
    case taskpkg.StatusComplete:
        // 仅当上游明确没有下一页时到达这里
    case taskpkg.StatusBudgetExhausted:
        // 未完成，预算已耗尽
    case taskpkg.StatusCursorCycle:
        // 未完成；result.Cycle 含有完整游标环和每页证据
    }
    break
}
```

`Progressed` 只在出现新的非置顶业务键或新的下一页游标时为真。非置顶新键会进入采集集合；置顶键会去重并计入集合，但不会单独构成进展。重复的置顶边界、空页和重复业务键都不会触发完成。

批量核对可调用 `taskpkg.Evaluate(pages, budget)`。输入页耗尽但未见明确无下一页时，`Status` 仍为 `continue` 且 `Complete` 为 `false`。

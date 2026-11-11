# hazptr/model：危险指针显式可见性事件模型

面向 Go 插桩宿主的可导入包，用**显式内存可见性事件**验证无锁对象池中的
危险指针（hazard pointer, HP）保护是否真的阻止提前回收。模型不假定顺序一致
（sequential consistency）：所有跨线程可见性都必须由显式发布事件驱动，穷举
这些事件在两线程调度中的位置，即可复现“迟到发布”等弱内存交错并返回违规
见证（witness）。

## 事件模型

每个线程的程序是一串保持线程内顺序的 `Op`，由穷举器交错调度。

| 事件 | 宿主对应动作 | 可见性语义 |
| --- | --- | --- |
| `OpLoadRoot` | 线程读根 | 只读已发布的根，写入本地寄存器 |
| `OpProtect` | 写本地 HP 槽 | 仅暂存（staging），其他线程不可见 |
| `OpFlushHP` | 发布栅栏 | 暂存 HP 才进入全线程可见的保护集合 |
| `OpVerifyRoot` | 复读根并比较 | 一致后才允许解引用；不一致则中止重试 |
| `OpDeref` | 解引用对象 | 无一致复读或对象已回收即违规 |
| `OpClearHP` + `OpFlushHP` | 退出保护 | 先暂存清除，再发布清除 |
| `OpRetire` | 退役对象 | 进入退役集合，尚未释放 |
| `OpScan` | 回收扫描 | 固定一个扫描代次，快照退役集合并扫描**全部**线程的可见 HP |
| `OpSetRoot` / `OpFlushRoot` | 改根并发布 | 同样分暂存与发布两步 |
| `OpAlloc` | 分配 | 可指定复用已释放地址，用于 ABA 场景 |

## 强制的协议不变量

- **一致复读门禁**：未通过 `OpVerifyRoot` 的寄存器解引用报
  `DEREF_BEFORE_CONSISTENCY`。
- **全线程扫描 + 固定代次**：一次 `OpScan` 递增扫描代次，并只根据该时刻的
  退役集合快照与 HP 快照做决定；漏掉线程且该线程保护会改变释放决定时报
  `PARTIAL_SCAN`。
- **提前回收拦截**：任何被释放地址若仍出现在某线程可见 HP 中，报
  `PREMATURE_RECLAIM`；读者随后解引用报 `USE_AFTER_FREE`。
- **地址复用识别**：每个地址维护对象代次（identity/generation）。纯地址 HP
  在复用语义下命中不同代次时报 `ADDRESS_REUSE_ABA`；开启 `TrackIdentity`
  （带标签 HP）后同一地址的不同代次不会互相冒充。

## 快速开始

```go
import "example.org/netsec/uid1243/hazptr/model"

report, err := model.Enumerate(
    []int{0, 1},
    map[string]string{"head": "A"},
    model.DefaultConfig(),
    model.Programs{ /* 每线程的显式事件序列 */ },
)
if !report.Safe() {
    for _, w := range report.Witnesses {
        fmt.Print(model.RenderWitness(w)) // 打印完整交错见证
    }
}
```

真实插桩宿主可逐步驱动同一台状态机，而不是一次性穷举：

```go
m := model.NewMachine([]int{1}, roots, model.DefaultConfig())
rec := model.NewRecorder(m, 1)
rec.LoadRoot("head", "p", "")
rec.Protect(0, "A", "")
rec.PublishProtection("")      // 缺少这一步 = HP 对回收线程不可见
rec.VerifyRoot("head", "p", "")
rec.Deref("p", "")
```

## 穷举与见证

`Enumerate` 保留每个线程的程序顺序，枚举“下一步由哪个线程执行”的全部合法
选择，并对 `(机器状态, 各线程程序计数器)` 做指纹去重。不可行分支（例如对
非存活地址建立保护）以 `ErrInfeasible` 剪枝，不计为协议违规。每个违规类型
保留一条代表性完整调度，`Report` 同时给出状态数、转移数、完成调度数和
各违规出现次数。

运行：

```sh
go test ./hazptr/model/ -v          # 六个小状态穷举场景
go run ./cmd/hazptr-demo            # 打印迟到发布/地址复用/部分扫描见证
```

## 覆盖的两线程场景

- 迟到发布：HP 发布栅栏落在回收扫描之后，解引用命中已释放对象。
- 缺发布：只写本地 HP 槽、从不发布，扫描无法看到保护。
- 地址复用：对象释放后同一地址被重新分配，旧指针解引用识别为 ABA。
- 部分扫描：回收方只扫描自己，忽略读者线程的可见保护。
- 合规基线：完整协议在全部交错下保持安全（不一致复读的分支中止重试）。

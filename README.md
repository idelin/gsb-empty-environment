# 可嵌入的借用授权宿主（C++17）

这个仓库实现了一个只依赖抽象对象、代（generation）和半开区间 `[begin, end)`
的借用授权组件。核心代码不持有也不解引用任何真实对象指针；真实的字节只出现在
用于验收的“小对象逐字节解释器”中。

## 目录

- `include/borrow/borrow_authority.hpp`：帧树、租约、暂停票、取消、移动、区间证书。
- `include/borrow/history.hpp`：事件段/缺段回放、三值判定、历史证书。
- `include/borrow/byte_interpreter.hpp`：小对象逐字节读写核对器。
- `include/borrow/borrow.hpp`：统一入口。
- `tests/test_main.cpp`：覆盖题面全部场景的可执行测试。
- `examples/demo.cpp`：缺段补全前后判定的最小演示。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug
cmake --build build -j
(cd build && ctest --output-on-failure)
./build/borrow_demo
```

整个组件是 header-only 的，也可以直接：

```cpp
#include "borrow/borrow.hpp"
```

## 运行时模型

### 对象、代与区间

`ObjectRef{id, generation}` 是版本化对象身份。`Range{begin, end}` 是半开区间。
`IntervalSet` 自动合并、求交、求差，并保持排序且不相接。

`Authority::move_object(id)` 会把对象代加一，并把旧代上的所有租约标记为非活动。
旧代 `ObjectRef` 之后的访问返回 `Status::StaleGeneration`；新代从宿主根能力重新
开始授权。

```cpp
borrow::Authority host;
auto ref = host.define_object(/*id=*/1, /*size=*/16);
auto fresh = host.move_object(1);   // fresh->generation == 1
```

### 帧与调用链

帧 `0` 是宿主根帧，始终活动，拥有完整能力。`enter(parent)` 可以在父帧尚未返回时
创建子帧，因此支持重入；`enter_into` 可以指定稳定帧号，便于事件回放。

```cpp
FrameId parent = host.enter();
FrameId child  = host.enter(parent);   // 父回调尚未 leave
```

帧状态为 `Active`、`Suspended`、`Cancelled`、`Returned` 之一。`leave(frame)` 等价于
正常归还：要求没有活动/挂起子帧，撤销本帧租约并把帧标为 `Returned`。

### 共享/独占租约与权限

`BorrowMode::Shared` 给读权限，`BorrowMode::Exclusive` 给读写权限。权限不是只看
当前帧：

- 祖先链上的共享租约会把它后面的整条调用链降为只读；
- 祖先链上的独占租约允许后代读写；
- 兄弟帧、后代帧或无关帧的独占租约阻止重叠访问；
- 兄弟帧、后代帧或无关帧的共享租约阻止重叠写；
- 本帧自己的租约是能力而不是冲突。

```cpp
auto ref = host.define_object(1, 8);
FrameId parent = host.enter();
host.acquire(parent, ref, {0, 8}, BorrowMode::Exclusive);

FrameId child = host.enter(parent);
host.acquire(child, ref, {0, 4}, BorrowMode::Shared);

// 子帧的共享租约同时收窄它自己和父帧：
assert(!host.can_access(child,  ref, {0, 4}, AccessKind::Write).allowed);
assert(!host.can_access(parent, ref, {0, 4}, AccessKind::Write).allowed);
assert( host.can_access(parent, ref, {4, 8}, AccessKind::Write).allowed);
```

`can_access` 只检查访问；`can_borrow`/`acquire` 还会检查能否建立新的子租约。

### 暂停、恢复票与取消

`suspend(frame)` 不归还任何租约，只把帧标为 `Suspended` 并发出
`SuspendTicket`。挂起帧不能执行访问，但它的租约仍然约束其他帧。活动子帧必须先
暂停，父帧才能暂停，因此可以形成一条完整的挂起调用链。

`resume(ticket)` 要求：

- 票属于当前宿主；
- 票没有被消费；
- 帧仍处于挂起状态；
- 票的 branch、lineage hash、suspension epoch 与帧一致；
- 所有祖先当前都是 `Active`。

`resume_chain({tickets...})` 额外要求票顺序构成父子链，且所有票的 branch 相同。
因此来自不同历史分支的票不会被拼成一条“合法调用链”；`resume_as(ticket, other)`
也不允许把票移植到另一个帧。

```cpp
auto a = host.suspend(left);
auto b = host.suspend(right);
host.cancel(left);              // 只撤销 left 及其实际后代
assert(!host.ticket_valid(a.ticket));
assert( host.ticket_valid(b.ticket));
host.resume(b.ticket);          // 另一个挂起回调恢复
```

取消已经 `Returned`/`Cancelled` 的后代不会被重写成新的取消历史；只有该帧当前
实际存在的子树被撤销。

### 部分归还

`release(frame, ref, range)` 只从该帧自己的租约里扣除交集，可把一条租约切成两段，
也可以只归还半条区间。父帧租约不会被子帧归还动作影响。

```cpp
host.acquire(owner, ref, {0, 8}, BorrowMode::Exclusive);
host.release(owner, ref, {4, 8});
// owner 仍持有 [0,4)
```

`evaluate(..., QueryKind::Return)` 会返回：

- `allowed`：该帧确实拥有的子区间；
- `forbidden`：该帧没有租约的子区间；
- 整个区间既非全允许也非全禁止时，判定为 `Undecided`。

### 查询与证书

`QueryKind` 支持 `Read`、`Write`、`Move`、`Return`。

```cpp
QueryReport q = host.evaluate(frame, ref, {0, 8}, QueryKind::Write);
QueryCertificate cert = host.certificate(frame, ref, {0, 8}, QueryKind::Write);
assert(cert.verify(host));
```

`QueryCertificate` 保存查询、判定、`allowed/forbidden/undecided` 子区间、阻塞帧和
完整的 `StackCertificate`。`StackCertificate` 内含排序后的对象、帧、租约、恢复票
视图和摘要；`internally_valid()` 可只靠证书自身重算摘要，`verify(authority)` 再
把证书与当前宿主快照逐项比较。

## 事件段、缺段与三值历史

`HistoryProblem` 的输入是：

- 初始版本化对象集合；
- 按时间顺序排列的 `Segment`；
- 每个段可有对象版本约束 `objects` 和可选的 `expected_stack_digest`；
- `missing=true` 的段只给出摘要 `summary` 和可选的操作类别集合 `categories`。

### 缺段解释与“同摘要同解释”

每个缺段从宿主登记的 `Category` 中选一个解释。内置类别包括 `noop`、`read`、
`write`、`shared`、`exclusive`、`release`、`move`、`suspend`、`resume`、
`cancel`、`enter`、`leave`；也可以注册自定义类别：

```cpp
p.register_category(Category{
    "read-then-write", "host registered category",
    [](const Segment& s) {
        return std::vector<Event>{
            Event::read(s.frame, s.object, s.range),
            Event::write(s.frame, s.object, s.range),
        };
    }});
```

同一 `summary` 引用的所有缺段共享一个解释。若各段给出的候选集合不同，取交集；
若交集为空则问题不可满足。未设置摘要的缺段各自独立。也可以用
`fix_summary(summary, category)` 在后来补定解释，或用
`fill_gap(segment_id, events)` 直接把某个缺段替换成观察事件。

`HistoryQuery::after_segment` 可以把查询点固定在某个段结束处；该段之后的观察段
和缺段不会参与这次判定。

### 全部相容历史

求解器枚举所有解释组合，对每个组合从初始宿主重新回放。某个解释只有在所有
观察操作都合法、对象版本匹配、栈摘要匹配时才成为一条相容历史。之后对查询区间
汇总：

- `Allow`：每条相容历史都允许整个区间；
- `Forbid`：每条相容历史都禁止整个区间；
- `Undecided`：相容历史之间不一致，或区间只有一部分被共同允许/禁止。

证书还逐段给出：

- `common_allowed`：所有相容历史都允许的区间交集；
- `common_forbidden`：所有相容历史都禁止的区间交集；
- `undecided`：查询区间中未被上述两者覆盖的部分；
- 每条见证历史的解释选择、判定、允许/禁止区间、栈摘要和回放摘要；
- `truncated`、`inconsistent` 和解释数量。

`HistoryCertificate::verify(problem)` 会重新求解并逐条重放见证，不信任证书里的
断言。`HistoryProblem::materialize(choices)` 可以把某个解释直接回放成一份
`Authority` 状态，交给逐字节解释器继续验收。

`QueryCertificate::to_json()`、`StackCertificate::to_json()` 和
`HistoryCertificate::to_json()` 提供稳定的 JSON 投影，便于把区间、选择、见证和
摘要交给外部工具复核。

### 最小例子

```cpp
HistoryProblem p;
p.add_object(1, 8, 0);

Segment setup;
setup.events = {
    Event::enter(0, 10),
    Event::exclusive(10, 1, {0, 8}),
    Event::enter(10, 11),
    Event::enter(10, 12),
};
p.add_segment(setup);

Segment gap;
gap.summary = "gap-A";
gap.frame = 11;
gap.object = 1;
gap.range = {0, 4};
gap.categories = {"noop", "shared"};
p.add_gap(gap);

HistoryQuery q;
q.frame = 12;
q.object = 1;
q.range = {0, 4};
q.kind = QueryKind::Write;

auto before = p.solve(q);          // Undecided：noop 允许，shared 禁止
p.fix_summary("gap-A", "shared");
auto after = p.solve(q);           // Forbid：后来补定缺段使未决变确定
assert(before.verify(p));
assert(after.verify(p));
```

## 小对象逐字节解释器

`ByteMemory` 保存一个很小的字节数组，但每次读/写都先对单字节区间调用
`Authority::can_access`。它不绕过授权：

```cpp
Authority host;
ByteMemory memory(host);
auto ref = memory.create(1, 8);
FrameId f = host.enter();
host.acquire(f, ref, {0, 8}, BorrowMode::Exclusive);

std::vector<std::uint8_t> bytes = {1,2,3,4,5,6,7,8};
assert(memory.write(f, ref, {0, 8}, bytes));
assert(memory.read(f, ref, {0, 8}).bytes == bytes);

host.release(f, ref, {4, 8});
// 另一个帧现在只能写 [4,8)，写 [0,4) 会在第 0 字节失败。
```

测试用这个解释器核对了：

- 父回调未返回时的重入；
- 移动后旧代失效、新代保留字节；
- 兄弟帧取消互不影响；
- 只归还半区间；
- 缺段补全前后，三值判定从 `Undecided` 变成确定值。

## 设计约束

- 核心授权不分配、不读写真实对象，只保存 `id/generation/range/mode`。
- 区间是半开区间；空区间不产生租约。
- 帧号、租约号、票号由宿主单调分配；`enter_into` 可指定稳定帧号用于回放。
- 回放是确定性的：摘要只依赖排序后的对象、帧、租约和事件文本，不依赖哈希表
  遍历顺序或宿主地址。
- 历史枚举默认上限为 1,000,000 个解释组合；超过时证书标记 `truncated=true`，
  不会把不完整枚举冒充成全体相容历史。

# roomdelivery

可导入的 Go 群聊房间投递组件。它把房间状态、身份揭示和逐接收实例投递回执放在同一模块中，并提供独立于宿主运行态的历史视图重建函数。

```go
import roomdelivery "prescreen.local/ddd-10531-v3"
```

## 基本流程

```go
host := roomdelivery.NewHost("room-1")

alice, _ := host.Join(roomdelivery.JoinRequest{
    Principal:   "alice",
    DisplayName: "alice",
    RealJID:     "alice@private.example",
})
bob, _ := host.Join(roomdelivery.JoinRequest{
    Principal:   "bob",
    DisplayName: "bob",
    RealJID:     "bob@private.example",
})

dispatch, _ := host.Send(roomdelivery.SendRequest{
    Author:      alice,
    Targets:     []roomdelivery.MembershipRef{bob},
    MessageType: roomdelivery.MessageTypeBody,
    Body: roomdelivery.TextBody("hello").WithIdentity("sender", roomdelivery.IdentityRef{
        Principal:  alice.Principal,
        Generation: alice.Generation,
        Instance:   alice.Instance,
    }),
})

_ = host.Publish(dispatch.Receipts[0].ID)

view, _ := roomdelivery.RebuildRecipientView(host.History(), bob)
encoded, _ := roomdelivery.CanonicalJSON(view)
```

`Join` 为每次入房分配唯一 `MembershipInstanceID` 和单调递增的主体代次。`Rejoin` 必须创建新实例和新代次；旧实例即使主体和昵称相同也不会被复用。显示名本身没有唯一性约束。

## 不变量

- 每次入房、改名、踢出、重入、揭示和撤销都写入带房间版本与主体代次的 `Event`。
- `Send` 创建一条逻辑消息和每个目标一份 `Receipt`。回执冻结原入房实例、目标实例、消息类型、引用关系和当时 `IdentityPolicy`。
- 纠正、撤回、离线发布和引用都通过回执范围校验；接收目标重入后，旧回执不能投给新实例。
- 揭示绑定主体代次和目标入房实例。主体或接收方重入都不会继承旧揭示。
- `ExpiresVersion` 为排他上界；`0` 表示不按版本自动过期。
- `Publish` 只从冻结策略取值，既不自动采用新揭示，也会在发布时重新检查原本会披露的身份是否已被撤销或过期。被撤销的含身份排队回执转为 `ReceiptBlocked`，不会晚到生效。
- 尚未发布时发生的纠正与撤回会反映在之后的发布中；离线补投按公共消息顺序重建，而不是按发布先后重排。
- 视图中的作者、正文属性、引用、人数统计和错误回执统一经过策略渲染。结构化身份字段使用不含 JID 的稳定别名；自由文本中的未授权真实 JID 替换为 `[redacted-jid]`，不会因地址值不同而产生不同的可见输出。
- `RebuildRecipientView` 只读取 `History` 与 `Receipts`，不读取宿主当前授权表。新揭示因此不能回填旧回执；引用使用被引用回执自己的冻结策略。

## 视图重建

```go
func RebuildRecipientView(history History, viewer MembershipRef) (RecipientView, error)
```

`ReconstructRecipientView` 和 `BuildRecipientView` 是同一函数的别名；`Host.View` 则是基于当前历史的便捷入口。

`History.WithRealJID(instance, newJID)` 可生成仅改动一个私有入房地址的历史副本，用于验证同一接收方的 `CanonicalJSON` 输出保持不变。若该身份已被明确揭示，地址变化才会改变输出。

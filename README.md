# 低权限进程抢占全局命名对象

系统服务启动时按固定名字打开互斥量和共享段，低权限进程若先创建同名对象，可让服务连接到攻击者控制的对象。给Go服务宿主实现题内对象命名空间准入包：名字由会话范围{Global,SessionID}、规范UTF-16名称和对象类型组成，名称拒绝NUL、点段、尾部空格/点及大小写折叠后的冲突；对象记录创建者SID、创建者完整性、类型、对象代次、不可变安全描述符和随机128位creationNonce。服务策略为每个预期名固定类型、允许创建者SID集合、最低完整性和安全描述符摘要；OpenExisting必须逐项核对，类型错配不能退回Create。CreateNew使用create-if-absent语义并提交服务生成nonce；若名称已存在则不覆盖。服务创建后保存(objectID,代次,nonce,描述符摘要)，每次Use都重验且宿主回调只接通过重验的对象句柄；删除重建产生新代次，旧句柄拒绝。对需要跨会话的Global作用域对象，低权限创建者一律拒绝，即使DACL看似宽松。用并发创建BFS复现攻击者先占名、类型混淆、大小写碰撞、删除重建及会话间同名，正确实现不得对攻击者对象执行Lock或Map回调。

## 使用方式

```go
ns := netsec.NewNamespace()
svc := netsec.NewService(ns, netsec.Creator{
    SID:       netsec.LocalSystemSID,
    Integrity: netsec.IntegritySystem,
})

name := netsec.MustName(netsec.GlobalScope(), `svc\state`, netsec.ObjectTypeSection)
policy := netsec.MustPolicy(name, []string{netsec.LocalSystemSID},
    netsec.IntegrityHigh, `D:(A;;GA;;;SY)`)
if err := svc.RegisterPolicy(policy); err != nil {
    // fail closed
}

// OpenExisting never creates and never falls back after a type mismatch.
bound, err := svc.OpenExisting(name)
if err != nil {
    // fail closed; do not open or create an attacker-owned object
}

// Every Use rechecks policy, generation, nonce and descriptor digest.
err = bound.Map(func(h netsec.VerifiedHandle) error {
    // h is only delivered after successful revalidation
    return nil
})
```

`CreateNew` generates the 128-bit nonce, calls `Namespace.CreateIfAbsent`, then
saves `(objectID, generation, nonce, descriptor digest)`. If the folded name
already exists, including as a different case or object type, it returns an
error and leaves the existing object untouched. Deleting and recreating a name
advances its generation, so every older `BoundObject` is rejected.

## 安全不变量

- Name validation rejects NUL, empty/dot path components, trailing spaces or
  dots, `/`, malformed UTF-16 and case-folded collisions.
- `OpenExisting` checks scope, folded name, fixed type, creator SID, creator
  integrity and descriptor digest. A type mismatch is terminal.
- `CreateNew` is create-if-absent under one namespace lock and never overwrites.
- `Use`, `Lock` and `Map` verify before invoking the callback while the
  namespace read lock prevents delete/recreate from interleaving.
- Global objects require creator integrity of at least `IntegrityHigh` even if
  the SID allowlist and DACL digest would otherwise permit the creator.
- Session IDs are part of the namespace key, so identical text in different
  sessions remains independent.

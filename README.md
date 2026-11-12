# Yama ptrace范围判定

Linux 的 Yama ptrace_scope 限制进程调试同用户的其他进程，攻击者进程想附着到浏览器或 ssh-agent 读取其内存。为Go沙箱管理组件实现供 ptrace 授权模拟调用的判定包：先通过 uid 与能力的基本检查；scope 0 同 uid 即可，1 仅目标是调用者后代，或目标已用 PR_SET_PTRACER 指定调用者（或任意进程），2 仅持有 CAP_SYS_PTRACE，3 一律禁止；PTRACE_TRACEME 不受 scope 1 限制；进程树随 fork 与 exit 变化，孤儿被重新挂到 init 后不再属于原祖先。用 scope 各级、后代判定、PR_SET_PTRACER 指定和孤儿重挂验证。

## 判定规则

每次请求先做基本权限检查，再按 `ptrace_scope` 施加 Yama 限制：

1. **基本检查**：调用者与目标同 uid，或调用者持有 `CAP_SYS_PTRACE`，否则直接拒绝（`ReasonCredential`）。
2. **scope 0 `ScopeClassic`**：通过基本检查即可，同 uid 的兄弟进程（如浏览器）也能被附着。
3. **scope 1 `ScopeRestricted`**：目标必须是调用者的严格后代，或目标通过 `PR_SET_PTRACER` 指定了该调用者 / `PR_SET_PTRACER_ANY`；持有 `CAP_SYS_PTRACE` 可绕过关系限制。
4. **scope 2 `ScopeAdminOnly`**：仅 `CAP_SYS_PTRACE` 持有者可 ptrace，同 uid 父子关系也不够。
5. **scope 3 `ScopeNoAttach`**：一律禁止，任何能力都无法绕过。
6. `PTRACE_TRACEME` 不受 scope 1 的关系限制；但 scope 2 仍要求能力、scope 3 仍禁止。

## 进程树语义

`ProcessTree` 以 init 为根，模拟 `fork`/`exit`：

- `Fork`/`ForkAs` 建立父子关系，子进程继承 uid 与能力集合，`PR_SET_PTRACER` 授权不继承。
- `Exit` 删除进程并把其直接子进程重挂到 init；孙进程经新父链间接挂到 init，从此不再是已退出进程或原祖先的后代。
- `SetPtracer`/`ClearPtracer` 模拟 `prctl(PR_SET_PTRACER, ...)`，目标退出时授权随之清除。
- 所有方法带 `sync.RWMutex` 保护，可并发访问；查询返回进程快照，修改快照不会回写树。

## 快速上手

```go
tree := NewProcessTreeWithInit(1, 1000)

attacker, _ := tree.Fork(1)                 // uid 1000
browser, _ := tree.Fork(1)                  // 兄弟进程，非后代

auth := NewAuthorizer(tree, ScopeRestricted)
auth.CanAttach(attacker.PID, browser.PID)   // Allowed=false, ReasonNotDescendant

_ = tree.SetPtracer(browser.PID, attacker.PID)
auth.CanAttach(attacker.PID, browser.PID)   // Allowed=true

// scope 2 下只有 CAP_SYS_PTRACE 放行
admin := NewAuthorizer(tree, ScopeAdminOnly)
admin.CanAttach(attacker.PID, browser.PID)  // Allowed=false, ReasonMissingCap
```

## 测试

```sh
go test -race -cover ./...
```

测试覆盖四级 scope、严格后代判定、`PR_SET_PTRACER` 指定与 `PR_SET_PTRACER_ANY`、
能力绕过、孤儿重挂后祖先关系失效、`PTRACE_TRACEME` 特例以及基本 uid 检查。

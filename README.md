# Tetragon 跟踪策略同族调用覆盖

Tetragon 的跟踪策略在内核函数上挂钩，按参数选择器放行或终止进程。攻击者改用同族的其他系统调用（`openat2` 代替 `open`）、相对路径或带 `..` 的路径，即可绕过只钩住某一个调用的策略。本包 `netsec` 供 Go 策略下发服务在**策略保存前**调用，做同族调用覆盖校验。

## 输入

- `Policy`：钩子列表，每个 `Hook` 含调用名、参数类型（`Param`）、选择器（`Selector`：参数上的 `Prefix`/`Equal`/`Postfix`，条件间 AND，选择器间 OR）和动作（`Sigkill` 或 `Override`，后者带可选 `ReturnCode`）。
- `Target`：组织要求保护的目标，含路径、是否目录、访问方式（`open`/`rename`/`truncate`）。
- `FamilyTable`：同族调用表，默认实现 `DefaultFamilyTable` 覆盖 `open`/`openat`/`openat2`/`creat`、`rename`/`renameat`/`renameat2`、`truncate`/`ftruncate`，可自定义替换。

## 校验规则

入口 `Validate(policy, targets, table) *Report`，规则如下：

1. **同族覆盖**：每个保护目标在其同族调用表中的每个调用都必须有有效钩子，否则按调用输出 `UNCOVERED_CALL`（error），报告“某目标经哪个同族调用仍未被覆盖”。
2. **路径规范化**：所有路径先经 `path.Clean` 解析 `.`、`..` 与重复斜杠；相对路径与无法消掉的前导 `..` 会保留，不会被绝对路径 Prefix 误判为覆盖。
3. **Prefix 标注**：Prefix 只对规范化后的路径有效，且按路径段匹配（`/srv/secret` 不会误命中 `/srv/secret2`）；只要目标评估中用到 Prefix，就输出 `PREFIX_NORMALIZED`（info），`Finding.Normalized=true` 供报告显式标注。
4. **Postfix 限制**：目录目标若仅由 Postfix 选择器命中，输出 `POSTFIX_ONLY_DIRECTORY`（error）；普通文件不受限，Prefix/Equal 与 Postfix 组合也不受限。
5. **Override 返回码**：Override 钩子缺返回码输出 `OVERRIDE_NO_RETURN`（error，拒绝保存）；返回码为 `0` 输出 `OVERRIDE_RETURN_ZERO`（warning，提示调用方可能视为成功而放行），且该钩子不计入有效覆盖；非 0 返回码与 Sigkill 才形成覆盖。
6. **参数合法性**：选择器引用未声明或非字符串参数时输出 `INVALID_SELECTOR_PARAM`（error），该选择器不参与匹配。

`Report.Rejected`（等价 `HasError()`）为 true 时下发服务应拒绝保存；`Coverage` 给出每个目标的已覆盖/未覆盖调用明细与规范化路径。

## 验证

```sh
go test ./... -race -cover
```

测试覆盖题目要求的四个场景及动作规则：

- 只钩 `openat`：`open`/`openat2`/`creat` 全部报未覆盖并拒绝保存；
- 相对路径 / 带 `..` 路径：验证规范化结果、Prefix 标注与前导 `..` 不被误覆盖；
- 仅 Postfix：目录报 `POSTFIX_ONLY_DIRECTORY`，文件与 Prefix+Postfix 组合不报；
- 完整覆盖：open 族与 rename 族全部调用有有效钩子时无 error；
- Override：缺返回码拒绝、返回码 0 警告且不覆盖、非 0 返回码可覆盖。

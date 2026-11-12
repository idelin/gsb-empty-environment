# GHCB响应校验

SEV-ES 客户机的 #VC 处理程序通过共享的 GHCB 页让宿主模拟 CPUID、MSR 与端口 I/O，宿主可回填任意值（Iago 攻击）。为C++17客户机固件实现供 #VC 处理程序调用的响应校验组件：请求前清空 valid_bitmap，响应只能包含该退出码允许的字段（CPUID 返回 rax、rbx、rcx、rdx，端口读返回 rax，端口写无返回）且对应位已置；sw_exit_info1 非 0 表示失败须按错误处理；scratch 区必须落在 GHCB 共享区内；端口读响应不得超出请求宽度，超出位清零；CPUID 结果的特性位须是调用方可信表的子集。用多余字段、缺位、失败码、scratch 越界和合法 CPUID 验证。

## 文件

- `ghcb_protocol.hpp`：GHCB 页布局与位图 ABI。4 KiB 页分为 2 KiB save area 与 2 KiB shared buffer；`valid_bitmap` 第 N 位对应 save area 偏移 `8*N` 的 qword，布局由 `static_assert` 锁定。
- `ghcb_response_validator.hpp` / `.cpp`：响应校验组件，无动态分配、无异常，可直接在固件 #VC 上下文中调用。
- `ghcb_response_validator_test.cpp`：自包含断言测试，覆盖多余字段、缺位、失败码、scratch 越界与合法 CPUID 子集等场景。

## 校验规则

1. `reset_for_request()`：请求发起前清空 `valid_bitmap` 及 `sw_*`、GPR 字段，宿主回填必须重新置位。
2. 公共头校验：`sw_exit_code` 有效位须置位且取值与请求一致；`sw_exit_info_1 != 0` 一律按宿主失败（`HypervisorError`）处理，不再信任同页数据。
3. 字段白名单（仅检查四个 GPR 数据字段）：
   - CPUID：`rax/rbx/rcx/rdx` 全部允许且全部必需。
   - 端口读：仅允许且必需 `rax`；端口写：无任何返回字段。
   - MSR 读：`rbx`（索引，请求字段）允许，`rcx`（值）必需；MSR 写：`rbx/rcx` 均为请求字段，无返回字段。
   - 置位但不在白名单 → `UnexpectedField`；必需但未置位 → `MissingField`。
4. scratch 校验：有效位须置位，且 `[sw_scratch, sw_scratch+size)` 完整落在 GHCB shared buffer 内，含加法回绕检查；越界返回 `ScratchOutOfRange`。
5. 端口读：按 `width_bytes`（1/2/4）生成掩码，`rax` 超出请求宽度的位清零后输出。
6. CPUID：按 leaf/subleaf 查调用方可信表；宿主值（含高 32 位）必须是可信条目的子集，否则返回 `UntrustedCpuidResult`，通过后输出经可信掩码收敛的值。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

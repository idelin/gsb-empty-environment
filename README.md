# Wasm起始函数陷阱

起始函数陷入后，实例仍然被交出去。起始函数若陷入或调用了非法间接函数，整个实例化失败，不返回实例。起始函数正常返回的实例可用。可导入的C++状态机接运行时送来的状态。让起始函数调用一个空的函数引用。

## 语义

`netsec::Engine::instantiate` 在实例化末尾运行起始函数：

- 起始函数陷入（如 `call_indirect` 命中空函数引用，或目标签名不匹配的非法间接调用）→ 整个实例化失败，`Instantiation::instance` 为空，不交出实例，`trap` 给出原因。
- 起始函数正常返回 → 实例化成功，返回的 `Instance` 可正常调用导出函数。

`netsec::StateMachine` 是可导入的 C++ 状态机：运行时通过 `feed()` 送入状态，起始函数经 `env.fsm_next` 取出状态、经 `env.fsm_accept` 交还给状态机。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

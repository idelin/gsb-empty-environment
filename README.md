# 网络切片绑定组件

`netsec` 是一个可导入的 C++20 切片绑定库。它在会话建立时一次性绑定
切片标识、签约标识、会话标识和用户永久标识（SUPI），并在之后每次资源
访问时逐项校验。组件采用严格切片隔离，切片替换、签约伪造、会话混用、
未签约切片和超配额访问都会被拒绝。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

库目标为 `netsec::netsec`，安装后可被其他 CMake 工程导入：

```cmake
find_package(netsec CONFIG REQUIRED)
target_link_libraries(core_session PRIVATE netsec::netsec)
```

## 集成方式

```cpp
#include "netsec.hpp"

netsec::SliceBindingManager binder;

// 管理面配置。session_quota 限制并发会话数，
// resource_quota 限制该切片累计可消费的资源单位数。
binder.register_slice("embb", 100, 10'000);
binder.register_subscription("sub-001", "imsi-460001234567890",
                             {"embb"});
binder.register_resource("video", "embb");

// 会话建立：四个标识必须与签约及切片策略一致。
netsec::SessionRequest request{"embb", "sub-001", "sm-ctx-001",
                               "imsi-460001234567890"};
const auto established = binder.establish(request);
if (!established) {
    // established.reason / established.code() 给出稳定的拒绝原因。
}

// 后续访问：四个标识逐项匹配，资源归属和配额原子校验。
request.resource_id = "video";
request.resource_units = 8;
const auto access = binder.validate(request);
if (access) {
    // 资源属于该会话绑定的 embb 切片，且配额已扣减。
}
```

`resource_slice_id` 可以显式传入，也可以只传 `resource_id` 由服务端资源
目录确定归属。无论哪一种方式，最终资源所属切片都必须等于会话绑定的
切片，不能由客户端声明覆盖。

## 强制规则

- 建立时校验切片策略已启用且严格隔离、签约存在并启用、SUPI 与签约
  一致、切片在签约白名单内。
- 同一会话标识只能建立一次，不能重新绑定到其他切片、签约或用户。
- 后续请求的切片、签约、会话、SUPI 任一项不同即拒绝，拒绝发生在配额
  扣减之前。
- 资源标识在服务端目录中的归属切片必须等于绑定切片；客户端提交的
  资源切片标识不能改变该归属。
- 会话配额和资源配额在同一把锁内检查并提交；失败的跨切片或超配额请求
  不会改变任何配额状态。
- 配额为 `0` 表示不允许，未显式设置时默认为 `unlimited_quota`。
  `release_session()` 归还会话配额槽，但已消费的资源配额不会退回。

`netsec::Decision` 同时提供 `allowed`、`ok()` 和稳定的 `reason` /
`code()`，便于会话组件记录审计日志或映射为 4xx/5xx 响应。管理器内部
有互斥保护，可供多个会话处理线程共同调用。

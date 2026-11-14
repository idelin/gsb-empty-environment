# 急停信号被伪造后设备继续运行

安全总线按急停报文停机，攻击者伪造复位或屏蔽急停，让设备在危险状态下继续运动。请实现供C++20安全总线组件调用的可导入急停完整性组件。急停回路使用双通道与差异校验，复位需现场确认与安全签名；屏蔽、伪造与单通道失效拒绝。急停状态硬接线优先。用急停伪造、复位伪造、单通道失效、合法复位和硬接线验证设备停机。

## 急停完整性组件

`estop_integrity.hpp` / `estop_integrity.cpp` 提供 `netsec::estop::EstopIntegrity`，
供 C++20 安全总线组件导入调用（链接 `netsec` 库）：

- 双通道急停回路，差异超过时间窗即故障停机并闭锁（覆盖单通道失效）。
- 总线报文携带单调序号与 HMAC-SHA256 签名；伪造、重放、屏蔽急停、
  总线清除急停一律拒绝并闭锁为安全停。
- 复位需现场确认（本地按钮）+ 有效安全签名 + 回路恢复 + 硬接线未动作。
- 硬接线急停优先级最高，任何总线报文与复位均不能覆盖。

验证（五个场景：急停伪造、复位伪造、单通道失效、合法复位、硬接线优先）：

```sh
cmake -S . -B build -DCMAKE_AR=/Library/Developer/CommandLineTools/usr/bin/ar \
      -DCMAKE_RANLIB=/Library/Developer/CommandLineTools/usr/bin/ranlib
cmake --build build
ctest --test-dir build --output-on-failure
```

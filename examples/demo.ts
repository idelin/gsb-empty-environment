/**
 * 运行样例：100 秒滑动窗口内“温度 > 80°C”的累计超温时间。
 *
 * 场景：t=10s 温度升至 85°C（进入告警），t=40s 降至 75°C（退出告警）。
 * 预期：t=50s 时累计 30s；窗口滑动使 t=10s 滑出后，超温时间从
 * t=110s 起每过 1s 精确扣减 1s，t=140s 平滑归零。
 */
import { SlidingWindowConditionIntegrator } from "../src/sliding-window-integrator.ts";

const WINDOW_MS = 100_000;
const integrator = new SlidingWindowConditionIntegrator(
  WINDOW_MS,
  (temperature) => temperature > 80,
);

integrator.addSample(10_000, 85); // t=10s：升至 85°C
integrator.addSample(40_000, 75); // t=40s：降至 75°C

const probes = [50, 100, 110, 115, 120, 125, 130, 135, 140, 150];
console.log("窗口宽度: 100s | 告警条件: 温度 > 80°C");
console.log("事件: t=10s 升至 85°C, t=40s 降至 75°C\n");
for (const seconds of probes) {
  const accumulated = integrator.accumulatedMs(seconds * 1_000);
  console.log(
    `t=${String(seconds).padStart(3)}s  累计超温时间 = ${(accumulated / 1_000)
      .toFixed(1)
      .padStart(5)}s`,
  );
}

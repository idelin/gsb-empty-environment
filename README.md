# buildplanner

从本地 JSON 读取包与构建输入（不联网），把包划分为可分阶段执行的共同编译组。

- 版本格式：`major.minor.patch`，可带 `-prerelease`（点分隔标识符，数字标识符禁止前导零）。
- 声明依赖构成编译图；强连通分量（Tarjan）即共同编译组。同阶段的环合并为单组，
  组内成员**没有拓扑顺序**，输出中明确标注 `unordered`，不假装有序。
- 生成器在阶段结束时产出 artifact，消费者在阶段开始时消费。若依赖分层迫使
  生成器阶段 >= 消费者阶段，则构成"生成器依赖自身产物"的跨阶段环：拒绝并给出
  具体阻塞证据（artifact、双方包@版本与阶段、依赖路径）。被波及的组及其下游
  标记为 BLOCKED，无关组仍保留可执行计划。

## 输入

目录下的多个 `*.json`，或单个 JSON 文件（对象或对象列表）：

```json
{
  "name": "core",
  "version": "2.1.0-alpha.3",
  "dependencies": ["base"],
  "generates": ["core.ir"],
  "consumes": ["base.ir"]
}
```

依赖与 artifact 必须能在本地解析，否则报错（不联网查包）。

## 运行

```sh
python3.11 -m buildplanner examples/packages_ok        # 退出码 0
python3.11 -m buildplanner examples/packages_blocked   # 退出码 1，打印阻塞证据
python3.11 -m buildplanner examples/packages_ok --json # 机器可读输出
```

## 测试

```sh
python3.11 -m unittest discover -s tests -v
```

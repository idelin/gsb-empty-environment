# 简化链接器（C++17）

演示符号裁决与归档成员按需加载的教学型链接器。

## 构建与运行

```sh
c++ -std=c++17 -Wall -Wextra -O2 -o linker linker.cpp
./linker examples/main.o examples/extra.o examples/libutil.a
./run_tests.sh   # 运行全部测试
```

## 输入格式

普通目标文件（无条件加载）：

```
module main
def strong main      # 强定义
def weak   log       # 弱定义
undef      helper    # 未定义引用
```

归档文件（成员按需加载，保持书写顺序）：

```
archive libutil.a
member util1
def strong helper
undef log2
end
end
```

## 裁决规则（仿 ELF）

- 同一符号多个强定义 => 冲突错误，列出全部来源，与输入顺序无关。
- 强定义覆盖弱定义；多个弱定义取先出现者。
- 归档成员仅当其**强定义**能满足当前未定义引用时才被加载；
  归档中的弱定义不触发加载。
- 加载新成员可能引入新的未定义引用，迭代直到没有新成员被加载（不动点）。
- 迭代结束后仍有未定义引用 => 报错并列出所有引用者，退出码非零。

## 输出

- 每个已加载模块及其加载原因（含第几轮迭代、为解析哪个符号、被谁引用）。
- 未被加载的归档成员及原因。
- 每个符号的最终来源（strong/weak + 定义模块）。

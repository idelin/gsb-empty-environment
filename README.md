# 符号表达式模式重写系统

纯 Python 3.11 标准库实现, 不依赖任何符号代数库。

## 特性

- **模式重写**: `PVar`(任意表达式)、`PNum`(仅有理数)、`PLit`(字面量)、`PApply`(复合模式), 同名变量须一致绑定
- **规则优先级与条件**: `priority` 大者优先, 同优先级按名称字典序(完全确定); `condition` 不满足时规则对本次匹配不可用
- **有理数精确计算**: 基于 `fractions.Fraction`, 全程无浮点
- **循环检测**: 规范化状态去重, 如 `a→b`、`b→a` 在第 2 步检测重复状态并报告首次出现的步索引
- **步数耗尽**: 超过 `max_steps` 仍未到规范形时报告 `EXHAUSTED`
- **完整变换链**: 每步记录规则名、重写位置(参数下标路径)、绑定、适用域说明、前后表达式
- **确定性可回放**: 前序最左最外选位, `Engine.replay` 可按 `(规则名, 位置)` 回放并校验每一步

## 文件

- `rewriter.py` — 表达式、模式、规则、引擎、解析/打印、标准算术规则
- `demo.py` — 5 个运行样例(精确化简、条件规则、循环、步数耗尽、优先级)
- `test_rewriter.py` — 27 个 unittest 用例

## 运行

```sh
python3 demo.py                 # 运行样例
python3 -m unittest test_rewriter -v   # 运行测试
```

## 快速示例

```python
from rewriter import Engine, parse, render_result, standard_rules

engine = Engine(standard_rules())
result = engine.rewrite(parse("add(1/2, 1/3)"))
print(render_result(result))    # => 5/6, 状态 normal_form

final, ok = engine.replay(result.initial, result.steps)  # 回放校验
```

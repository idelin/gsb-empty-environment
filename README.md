# 隔离领域函数边界

宿主通过 ShadowRealm 式的隔离接口执行不可信脚本，脚本回传的函数若直接暴露，调用方可经 .constructor 取得内部 Function 再逃出。写可导入的 TypeScript 边界包装组件：跨界只允许原始值与可调用值，调用值被包装成无原型、无 constructor 属性的函数，参数和返回值同样过滤，异常转成统一错误。用 node:vm 构造的两个领域测试 constructor 链、对象返回、getter 副作用、Symbol 和抛出自定义对象。

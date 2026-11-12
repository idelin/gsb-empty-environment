# 内置原型冻结锁定

插件宿主启动后，插件脚本仍可能通过 Object.prototype、Array.prototype 或 constructor.prototype 路径写入默认属性，影响后续所有请求。写可导入的 TypeScript 锁定组件：从 globalThis 沿自有属性遍历标准内置对象及其原型，包含 getter、setter、符号键和函数的 prototype，冻结后返回覆盖路径清单；宿主登记的可变数据记录作为例外。用严格模式写入、setPrototypeOf、JSON 解析出的 __proto__ 键合并和宿主例外验证原型未被改变。

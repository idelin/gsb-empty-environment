# Capacitor插件桥来源与原型键

混合应用的原生桥收到网页消息 {callbackId, pluginId, methodName, options}，攻击者页面或嵌入的 iframe 调用 Filesystem 等特权插件，或把 pluginId 设成 __proto__ 与 constructor。为TypeScript宿主实现可嵌入的桥分发组件：来源主机必须在 allowNavigation 内，*.example.com 只匹配子域而不匹配 example.com、evilexample.com 与 example.com.evil.net，iframe 一律无权调用；插件与方法走显式白名单，按自有属性查找而不是对象属性链；callbackId 须为短字母数字，同一 callbackId 只能完成一次，options 的键含 __proto__ 即拒。用原型键、仿冒域、iframe 和重复回调验证。

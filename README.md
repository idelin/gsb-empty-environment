# COOP与COEP头隔离门

攻击者借重复头或Report-Only值，诱使页面误判为跨源隔离。为TypeScript浏览器宿主实现可导入的隔离判定组件：COOP、COEP按Structured Fields解析，same-origin与require-corp同时有效才开放SharedArrayBuffer；重复头、解析失败、Report-Only不作数。用变形头测试。

# MV3主机权限最小化

攻击者可借过宽的host_permissions和web_accessible_resources读取站点或探测扩展。为TypeScript上架服务实现可导入的清单比对包：实现匹配模式覆盖判定（scheme、*.子域、路径通配、<all_urls>），按声明功能算最小模式集，报告超额项。用*://*.example.com/*覆盖example.com测试。

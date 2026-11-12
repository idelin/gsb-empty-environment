# Tauri capability路径作用域

桌面应用的 webview 加载了含第三方内容的窗口，攻击者让页面调用 fs 命令读 $APPDATA 之外的文件。为TypeScript宿主写可嵌入 IPC 层的 capability 判定组件：capability 的 windows 用 * 通配匹配窗口标签，permissions 带 allow 与 deny 路径作用域，deny 恒优先；路径先展开 $APPDATA、$HOME 等变量再规范化点段与重复斜杠，** 跨目录而 * 不跨；webview 来源不在 capability 的 remote.urls 内时一律拒绝，本地页面例外；动态创建的窗口标签若不匹配任何 capability 则无权限。用 .. 穿越、deny 覆盖、远程来源与未知标签验证。

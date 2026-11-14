# 子域设置的Cookie覆盖了父域会话

企业站把会话放在父域Cookie，攻击者控制的`evil.example.com`子域可设置`Domain=.example.com`同名Cookie，令父域请求带上攻击者的会话或顶掉用户的登录态。请实现嵌入TypeScript Web框架的可导入Cookie签发与接收组件。每个Cookie携带名称、值、Domain、Path、Secure、HttpOnly、SameSite、有效期和签发主体；接收时按前缀规则核对：`__Host-`必须无Domain且Path为`/`并仅Secure，`__Secure-`必须Secure，带Domain的Cookie只能由该域自身或更具体子域签发。同名多Cookie按路径长度与创建时间决定唯一有效值，拒绝无法归属的域Cookie；会话标识轮换后旧值立即失效。用子域覆盖父域、前缀缺失、路径兄弟重叠、端口与主机大小写、合法同域登出及登录轮换验证父域会话不被外部子域替换。

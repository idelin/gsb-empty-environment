# PHP禁用函数的调用门等效判定

PHP 的 disable_functions 只按名字禁用内置函数，攻击者用大小写变形、前导反斜杠、可变函数、回调字符串、数组回调，或借 putenv 加 mail 之类的间接路径仍能执行外部程序。为 Go 网关写供解释器扩展的调用钩子调用的调用门包：输入被调函数名（含命名空间前缀、大小写、调用方式）、回调解析结果与参数，统一规范化后与禁用集合比较；禁用集合展开 system、exec、passthru、shell_exec、popen、proc_open、pcntl_exec；putenv 设置 LD_PRELOAD 或 LD_LIBRARY_PATH 后再调用 mail、mb_send_mail 或 imap_open 一律拒绝；call_user_func 与 array_map 的回调字符串同样检查，Class::method 形式只查方法名；strlen 等无关调用放行。用 SyStEm、\system、回调字符串、putenv 后 mail 和普通调用验证。

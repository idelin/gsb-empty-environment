# GlobalPlatform安全通道降级防护

攻击者在EXTERNAL AUTHENTICATE里选低安全级别，或重放旧C-MAC命令。给Go安全元件宿主写供APDU分发调用的状态包：安全级别（C-MAC 0x01、C-DECRYPTION 0x02）只升不降；INSTALL(E6)、LOAD(E8)、DELETE(E4)要求二者同时开启；MAC链接值与计数器逐条推进，失败即作废通道；MAC函数由调用方注入。用低级别、重放、计数器回退验证。

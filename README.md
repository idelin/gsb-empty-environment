# Git签名推送证书核对

攻击者可重放旧推送证书，或在证书之外夹带分支更新。为Go Git服务的接收钩子实现可导入的signed push校验包：解析certificate version 0.1的pusher、pushee、nonce与更新行，nonce仅用一次，证书更新行与实际更新逐项相等，受保护分支只认许可pusher。用重放和增删ref测试，验签由回调提供。

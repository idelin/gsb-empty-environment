# iframe沙箱标志推导

攻击者页借allow-scripts加allow-same-origin，在沙箱框架内移除自己的sandbox，或让弹窗逃逸。为TypeScript宿主实现可导入的标志推导包：按令牌集合算有效标志与来源，srcdoc继承父源，与父同源且同含两令牌判不安全，弹窗默认继承标志，逃逸令牌须显式出现。用嵌套iframe测试。

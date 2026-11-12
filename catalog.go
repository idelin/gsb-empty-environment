package netsec

// DefaultRegistry 返回内置常见 SASL 机制的注册表。
// 属性在本地固定声明，服务端通告的同名机制只能引用这些属性。
func DefaultRegistry() *Registry {
	reg := NewRegistry()
	defaults := []Mechanism{
		{
			Name:                   "PLAIN",
			RequiredProtection:     ChannelTLS,
			SendsRecoverableSecret: true,
			IdentityBinding:        BindingServerCredential,
		},
		{
			Name:                   "LOGIN",
			RequiredProtection:     ChannelTLS,
			SendsRecoverableSecret: true,
			IdentityBinding:        BindingServerCredential,
		},
		{
			Name:                   "SCRAM-SHA-1",
			RequiredProtection:     ChannelNone,
			SendsRecoverableSecret: false,
			IdentityBinding:        BindingServerCredential,
		},
		{
			Name:                   "SCRAM-SHA-256",
			RequiredProtection:     ChannelNone,
			SendsRecoverableSecret: false,
			IdentityBinding:        BindingServerCredential,
		},
		{
			Name:                   "SCRAM-SHA-1-PLUS",
			RequiredProtection:     ChannelTLSChannelBinding,
			SendsRecoverableSecret: false,
			IdentityBinding:        BindingChannel,
		},
		{
			Name:                   "SCRAM-SHA-256-PLUS",
			RequiredProtection:     ChannelTLSChannelBinding,
			SendsRecoverableSecret: false,
			IdentityBinding:        BindingChannel,
		},
		{
			Name:                   "EXTERNAL",
			RequiredProtection:     ChannelTLS,
			SendsRecoverableSecret: false,
			IdentityBinding:        BindingChannel,
		},
	}
	for _, m := range defaults {
		if err := reg.Register(m); err != nil {
			panic(err)
		}
	}
	return reg
}

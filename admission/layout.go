package admission

import "errors"

// AdmitLayout 基于 ParseLayout 得到的布局执行准入判定。
//
// minSDKVersion 为目标最低系统版本（API Level）；verify 为 v2/v3 摘要范围
// 与签名密码学校验回调，存在 v2/v3 签名时不可为 nil。返回 nil 表示放行。
func AdmitLayout(layout *Layout, minSDKVersion int, verify DigestVerifier) error {
	if layout == nil {
		return errors.Join(ErrInvalidInput, errors.New("nil layout"))
	}
	return Admit(Input{
		First8:                 layout.First8,
		CentralDirectory:       layout.CentralDirectory,
		EOCD:                   layout.EOCD,
		FirstLocalHeaderOffset: layout.FirstLocalHeaderOffset,
		SigBlock:               layout.SigBlock,
		MinSDKVersion:          minSDKVersion,
		VerifyDigest:           verify,
	})
}

// AsInput 把解析布局转换为结构化输入，便于调用方补充或覆写字段后调用 Admit。
func (l *Layout) AsInput(minSDKVersion int, verify DigestVerifier) Input {
	return Input{
		First8:                 l.First8,
		CentralDirectory:       l.CentralDirectory,
		EOCD:                   l.EOCD,
		FirstLocalHeaderOffset: l.FirstLocalHeaderOffset,
		SigBlock:               l.SigBlock,
		MinSDKVersion:          minSDKVersion,
		VerifyDigest:           verify,
	}
}

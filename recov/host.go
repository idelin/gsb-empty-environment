package recov

import (
	"crypto/ecdh"
)

// HostCallback 是消息宿主提供的回调。只有在全部密码学验收通过后，
// 解封得到的恢复密钥才会被交给该回调；验收失败绝不会触发回调。
type HostCallback func(sessionID []byte, acct AccountID, inst InstanceID, recoveryKey []byte) error

// RecoverSession 运行完整验收，并在通过后用接收方 OPK 私钥解封恢复密钥，
// 最后把明文恢复密钥交给宿主回调。
//
// receiverOPKPriv 必须是与信封 OPKCommitment 对应叶子的一次性私钥（由宿主
// 在密钥库中按账户/实例/序号保存）。返回的错误可用 errors.Is 归类到各哨兵。
func (im *Importer) RecoverSession(env *SessionEnvelope, receiverOPKPriv *ecdh.PrivateKey, cb HostCallback) error {
	decision, err := im.Evaluate(env)
	if err != nil {
		return err
	}

	// 解封私钥对应的公钥承诺必须与信封承诺一致，防止错钥触发无意义回调。
	pub := receiverOPKPriv.PublicKey()
	got := OPKCommitment(pub, env.OPKIndex, env.Instance, env.Account)
	if !equalBytes(got, env.OPKCommitment) {
		return wrapErr(ErrUnwrap, "receiver private key does not match the committed OPK")
	}

	recoveryKey, err := UnwrapRecoveryKey(
		receiverOPKPriv, env.EphemeralPub, decision.SessionID, env.Nonce, env.WrappedKey)
	if err != nil {
		return err
	}
	return cb(decision.SessionID, env.Account, env.Instance, recoveryKey)
}

// RecoverFromPackage 对独立验收包先做无状态重放，再解封并回调宿主。
// 它适用于新设备/新宿主首次导入历史会话的场景。
func RecoverFromPackage(pkg *AcceptancePackage, receiverOPKPriv *ecdh.PrivateKey, cb HostCallback) error {
	if err := VerifyAcceptancePackage(pkg); err != nil {
		return err
	}
	decoded, err := decodeAllRecords(pkg)
	if err != nil {
		return err
	}
	im, envelope, err := packageImporter(pkg, decoded)
	if err != nil {
		return err
	}
	return im.RecoverSession(envelope, receiverOPKPriv, cb)
}

func decodeAllRecords(pkg *AcceptancePackage) ([]any, error) {
	decoded := make([]any, 0, len(pkg.Records))
	for _, rec := range pkg.Records {
		v, err := decodeRecord(rec)
		if err != nil {
			return nil, err
		}
		decoded = append(decoded, v)
	}
	return decoded, nil
}

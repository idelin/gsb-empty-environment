package netsec

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
)

// 各用途标签，确保不同用途的 MAC/KDF 域分离。
const (
	labelPortMAC    = "q0155/port-bind"
	labelRespMAC    = "q0155/response"
	labelTunnel     = "q0155/tunnel"
	labelExporter   = "q0155/exporter"
	labelConfirmKey = "q0155/confirm-key"
	labelInner      = "q0155/inner"
	labelConfirm    = "q0155/confirm"
	labelTranscript = "q0155/transcript"
)

func mac(key []byte, label string, blob []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(label))
	h.Write([]byte{0})
	h.Write(blob)
	return h.Sum(nil)
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// tunnelKey 由隧道双方随机数导出，每条隧道唯一。
func tunnelKey(cNonce, sNonce []byte) []byte {
	return mac(cNonce, labelTunnel, sNonce)
}

// exporterKey 对应 TLS exporter / EAP 隧道导出值。
func exporterKey(tk []byte) []byte { return mac(tk, labelExporter, nil) }

// confirmKey 由导出值派生，用于宿主对接入端的密钥确认。
func confirmKey(exporter []byte) []byte { return mac(exporter, labelConfirmKey, nil) }

// portMAC 由宿主用宿主-服务器共享密钥对物理端口实例签名，
// 服务器据此信任“本次交换来自该端口实例”。
func portMAC(hostKey []byte, portID string, inst []byte) []byte {
	return mac(hostKey, labelPortMAC, frame([]byte(portID), inst))
}

// exchangeDigest 是交换摘要：以端口身份与端口实例为起点，
// 按序吸收全部 EAP 消息。宿主与服务器各自独立计算并比对。
func exchangeDigest(portID string, inst []byte, msgs []EAP) []byte {
	h := sha256.New()
	h.Write([]byte(labelTranscript))
	h.Write(frame([]byte(portID), inst))
	for _, m := range msgs {
		h.Write(putBytes(nil, m.encode()))
	}
	return h.Sum(nil)
}

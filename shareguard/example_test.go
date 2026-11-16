package shareguard_test

import (
	"errors"
	"fmt"

	"example.org/netsec/q0835/shareguard"
)

// ExampleGuard 演示客户端在一次安全聚合会话中如何接入份额披露守卫，
// 以及恶意服务器试图对同一用户索取两类份额时守卫如何终止会话。
func ExampleGuard() {
	const threshold = 2

	keys := map[string][]byte{
		"carol": []byte("key-carol"),
		"dave":  []byte("key-dave"),
		"erin":  []byte("erin-key"),
	}
	verifier := shareguard.NewHMACVerifier(keys)

	guard, err := shareguard.NewGuard(threshold, verifier)
	if err != nil {
		fmt.Println("new guard:", err)
		return
	}

	// 广播密钥 -> 分享 -> 掩码输入：在掩码输入阶段记录存活集合 U2。
	must(guard.AdvanceTo(shareguard.PhaseShare))
	must(guard.AdvanceTo(shareguard.PhaseMaskedInput))
	u2 := []string{"carol", "dave", "erin"}
	for _, id := range u2 {
		must(guard.ObserveParticipant(id))
	}

	// 一致性阶段：校验服务器给出的 U3 与至少 t 个 U3 成员的签名。
	must(guard.AdvanceTo(shareguard.PhaseConsistency))
	u3 := []string{"carol", "dave"} // erin 在 U2 中但被判定掉线
	sigs := make([]shareguard.Signature, 0, len(u3))
	for _, id := range u3 {
		tag, err := verifier.SignAliveSet(id, u3)
		if err != nil {
			fmt.Println("sign:", err)
			return
		}
		sigs = append(sigs, shareguard.Signature{Signer: id, Value: tag})
	}
	must(guard.AcceptConsistencyStatement(u3, sigs))

	// 解密阶段：唯一一次披露请求，S 必须等于 U3，D 必须等于 U2\U3。
	must(guard.AdvanceTo(shareguard.PhaseDecryption))
	dropped := []string{"erin"}
	surviving := []string{"carol", "dave"}
	must(guard.AcceptDecryptionRequest(dropped, surviving))

	// 掉线用户披露成对种子份额，存活用户披露自掩码份额。
	must(guard.DisclosePairwise("erin"))
	must(guard.DiscloseSelfMask("carol"))
	must(guard.DiscloseSelfMask("dave"))
	fmt.Println("legitimate disclosures done, terminated =", guard.Terminated())

	// 攻击：服务器改口声称 erin 也存活，索要她的自掩码份额。
	err = guard.DiscloseSelfMask("erin")
	fmt.Println("attack blocked:", errors.Is(err, shareguard.ErrSessionTerminated))
	fmt.Println("root cause:", errors.Is(guard.TerminationReason(), shareguard.ErrDoubleDisclosure))

	// 会话终止后所有后续披露都被拒绝。
	err = guard.DisclosePairwise("erin")
	fmt.Println("further disclosure blocked:", errors.Is(err, shareguard.ErrSessionTerminated))

	// Output:
	// legitimate disclosures done, terminated = false
	// attack blocked: true
	// root cause: true
	// further disclosure blocked: true
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

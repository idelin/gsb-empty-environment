package keyrecover_test

import (
	"fmt"

	"prescreen.local/ps-0a938757dd/keyrecover"
)

func ExampleRecover() {
	key := [keyrecover.Width]byte{0x2b, 0x7e, 0x15, 0x16}
	var observations []keyrecover.Observation

	// 用已知本地密钥合成 6 组不同 (v,j,d) 的故障前后输出。
	for g := 0; g < 6; g++ {
		v := [keyrecover.Width]byte{byte(g * 17), byte(g*31 + 1), byte(g*47 + 2), byte(g*53 + 3)}
		obs, err := keyrecover.Simulate(key, v, g%keyrecover.Width, byte(1+g*9))
		if err != nil {
			panic(err)
		}
		observations = append(observations, obs)
	}

	result, err := keyrecover.Recover(observations)
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Status == keyrecover.StatusUnique)
	fmt.Printf("%x\n", result.Candidates[0].Key)

	w := result.Candidates[0].Witness[0]
	fmt.Println(keyrecover.Explains(observations[w.Group], result.Candidates[0].Key, w.J, w.D))
	// Output:
	// true
	// 2b7e1516
	// true
}

func ExampleWithDomains() {
	key := [keyrecover.Width]byte{0x10, 0x20, 0x30, 0x40}
	obs, err := keyrecover.Simulate(key,
		[keyrecover.Width]byte{0x01, 0x02, 0x03, 0x04}, 2, 0x5a)
	if err != nil {
		panic(err)
	}

	// 宿主限定每个密钥字节只在 4 个允许值中选择，包含真实字节。
	domains := [keyrecover.Width][]byte{
		{0x10, 0x11, 0x12, 0x13},
		{0x20, 0x21, 0x22, 0x23},
		{0x30, 0x31, 0x32, 0x33},
		{0x40, 0x41, 0x42, 0x43},
	}
	result, err := keyrecover.Recover([]keyrecover.Observation{obs}, keyrecover.WithDomains(domains))
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Status)
	// Output:
	// unique
}

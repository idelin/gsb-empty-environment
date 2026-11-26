package tls13

import "testing"

func TestRFC8448SimpleStageSecrets(t *testing.T) {
	cs := TLS_AES_128_GCM_SHA256
	ecdhe := mustHex(t, `
8b d4 05 4f b5 5b 9d 63 fd fb ac f9 f0 4b 9f 0d
35 e6 d6 3f 53 75 63 ef d4 62 72 90 0f 89 49 2d`)

	early := NewEarlySecret(cs, nil)
	assertHex(t, "early secret", early.Secret, `
33 ad 0a 1c 60 7e c0 3b 09 e6 cd 98 93 68 0c e2
10 ad f3 00 aa 1f 26 60 e1 b2 2e 10 f1 70 f9 2a`)
	assertHex(t, "early derived secret", early.DerivedSecret(), `
6f 26 15 a1 08 c7 02 c5 67 8f 54 fc 9d ba b6 97
16 c0 76 18 9c 48 25 0c eb ea c3 57 6c 36 11 ba`)

	handshake := early.HandshakeSecret(ecdhe)
	assertHex(t, "handshake secret", handshake.Secret, `
1d c8 26 e9 36 06 aa 6f dc 0a ad c1 2f 74 1b 01
04 6a a6 b9 9f 69 1e d2 21 a9 f0 ca 04 3f be ac`)

	master := handshake.MasterSecret()
	assertHex(t, "master secret", master.Secret, `
18 df 06 84 3d 13 a0 8b f2 a4 49 84 4c 5f 8a 47
80 01 bc 4d 4c 62 79 84 d5 a4 1d a8 d0 40 29 19`)
}

func TestRFC8448SimpleTrafficKeysAndFinishedKey(t *testing.T) {
	cs := TLS_AES_128_GCM_SHA256

	clientHS := mustHex(t, `
b3 ed db 12 6e 06 7f 35 a7 80 b3 ab f4 5e 2d 8f
3b 1a 95 07 38 f5 2e 96 00 74 6a 0e 27 a5 5a 21`)
	serverHS := mustHex(t, `
b6 7b 7d 69 0c c1 6c 4e 75 e5 42 13 cb 2d 37 b4
e9 c9 12 bc de d9 10 5d 42 be fd 59 d3 91 ad 38`)

	clientKeys := MustDeriveTrafficKeys(cs, clientHS)
	assertHex(t, "client hs key", clientKeys.Key, `db fa a6 93 d1 76 2c 5b 66 6a f5 d9 50 25 8d 01`)
	assertHex(t, "client hs iv", clientKeys.IV, `5b d3 c7 1b 83 6e 0b 76 bb 73 26 5f`)

	serverKeys := MustDeriveTrafficKeys(cs, serverHS)
	assertHex(t, "server hs key", serverKeys.Key, `3f ce 51 60 09 c2 17 27 d0 f2 e4 e8 6e e4 03 bc`)
	assertHex(t, "server hs iv", serverKeys.IV, `5d 31 3e b2 67 12 76 ee 13 00 0b 30`)

	assertHex(t, "server finished key", FinishedKey(cs, serverHS), `
00 8d 3b 66 f8 16 ea 55 9f 96 b5 37 e8 85 c3 1f
c0 68 bf 49 2c 65 2f 01 f2 88 a1 d8 cd c1 9f c8`)

}

func TestTrafficUpdateDerivationVector(t *testing.T) {
	cs := TLS_AES_128_GCM_SHA256
	current := mustHex(t, "47D7EA08397B5871154B0FE85584BCC30A87C69E84D69B56007C5B21F76493BA")
	updated, err := NextTrafficSecret(cs, current)
	if err != nil {
		t.Fatal(err)
	}
	assertHex(t, "traffic upd secret", updated, `
52 9e 21 f7 87 c3 08 96 de 36 1e b1 1c 37 9a 26
	1e a7 b8 5f 4c 86 68 47 51 2e 68 5b b8 20 a5 33`)
	keys := MustDeriveTrafficKeys(cs, updated)
	assertHex(t, "updated key", keys.Key, `dd 01 4d 8e 12 90 e3 2e 3f fa fa 1d 87 d5 a6 e6`)
	assertHex(t, "updated iv", keys.IV, `1e 87 1d f1 ae 73 62 b3 68 60 4a 3e`)
}

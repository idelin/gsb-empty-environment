package tls13

import "testing"

func TestACVPKeyScheduleVectors(t *testing.T) {
	cs := TLS_AES_128_GCM_SHA256
	psk := mustHex(t, "56288B726C73829F7A3E47B103837C8139ACF552E7530C7A710B35ED41191698")
	dhe := mustHex(t, "EFFE9EC26AA29FD750DFA6A10B944D74071595B27EE88887D5E11C84590B5CC3")
	clientRandom := mustHex(t, "E9137679E582BA7C1DB41CF725F86C6D09C8C05F297BAD9A65B552EAF524FDE4")
	serverRandom := mustHex(t, "23ECCFD030790748C8F8D8A656FD98D717F1B62AF3712F97211D2070B499F98A")
	finishedServerRandom := mustHex(t, "C750EDA6696CD101B142BD79E00E6AC8C5F2C0ABC78DD64F4D991326659E9299")
	finishedClientRandom := mustHex(t, "62A62FA75563ED4FDCAA0BC16567B314871C304ACF06B0FFC3F08C1797594D43")

	transcript := NewTranscript(cs)
	_, _ = transcript.Write(clientRandom)
	early := NewEarlySecret(cs, psk)

	secret, err := early.ClientEarlyTrafficSecret(transcript.Sum())
	if err != nil {
		t.Fatal(err)
	}
	assertHex(t, "client early traffic", secret, "3272189698C3594D18F58EFA3F12B638A249515099BE7A2FA9836BABE74F0111")

	secret, err = early.EarlyExporterMasterSecret(transcript.Sum())
	if err != nil {
		t.Fatal(err)
	}
	assertHex(t, "early exporter", secret, "88E078F562CDC930219F6A5E98A1CE8C6E5F3DAC5AC516459A96F2EF8F114C66")

	handshake := early.HandshakeSecret(dhe)
	_, _ = transcript.Write(serverRandom)

	secret, err = handshake.ClientHandshakeTrafficSecret(transcript.Sum())
	if err != nil {
		t.Fatal(err)
	}
	assertHex(t, "client handshake traffic", secret, "B32306C3CE9932C460A1FE6C0F060593974842036B96FA45049B7352E71C2AD2")

	secret, err = handshake.ServerHandshakeTrafficSecret(transcript.Sum())
	if err != nil {
		t.Fatal(err)
	}
	assertHex(t, "server handshake traffic", secret, "22787F8CA269D34BC549AC8BA19F2040938A3AA370D7CC9D60F720882B88D01B")

	master := handshake.MasterSecret()
	_, _ = transcript.Write(finishedServerRandom)

	secret, err = master.ClientApplicationTrafficSecret(transcript.Sum())
	if err != nil {
		t.Fatal(err)
	}
	assertHex(t, "client application traffic", secret, "47D7EA08397B5871154B0FE85584BCC30A87C69E84D69B56007C5B21F76493BA")

	secret, err = master.ServerApplicationTrafficSecret(transcript.Sum())
	if err != nil {
		t.Fatal(err)
	}
	assertHex(t, "server application traffic", secret, "EFBDB0C873C0480DA57307083839A8984BE25B9A8545E4FCA029940FE2800565")

	secret, err = master.ExporterMasterSecret(transcript.Sum())
	if err != nil {
		t.Fatal(err)
	}
	assertHex(t, "exporter master", secret, "8A43D787EE3804EAD4A2A5B32972F9896B696295645D7222E1FD081DDD939834")

	_, _ = transcript.Write(finishedClientRandom)
	secret, err = master.ResumptionMasterSecret(transcript.Sum())
	if err != nil {
		t.Fatal(err)
	}
	assertHex(t, "resumption master", secret, "5F4C961329C91044011ACBECB0B289282E0E3FED045CB3EA924DFFE5FE654B3D")
}

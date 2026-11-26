// Command doublesign-verify performs a fully offline verification of a
// double-signing conviction evidence file. It needs only the evidence JSON and
// the authority public key (hex); no private key, node database or host state.
package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"formal.local/w3-39e1170702/doublesign"
)

func main() {
	keyHex := flag.String("authority-key", "", "hex-encoded Ed25519 authority public key")
	flag.Parse()
	if flag.NArg() != 1 || *keyHex == "" {
		fmt.Fprintln(os.Stderr, "usage: doublesign-verify -authority-key <hex> evidence.json")
		os.Exit(2)
	}

	rawKey, err := hex.DecodeString(*keyHex)
	if err != nil || len(rawKey) != ed25519.PublicKeySize {
		fmt.Fprintln(os.Stderr, "invalid authority public key")
		os.Exit(2)
	}
	raw, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "read evidence:", err)
		os.Exit(2)
	}
	var ev doublesign.Evidence
	if err := json.Unmarshal(raw, &ev); err != nil {
		fmt.Fprintln(os.Stderr, "decode evidence:", err)
		os.Exit(2)
	}
	if err := doublesign.VerifyEvidence(ev, ed25519.PublicKey(rawKey)); err != nil {
		fmt.Println("REJECTED:", err)
		os.Exit(1)
	}
	fmt.Printf("VALID conviction: member=%s epoch=%d violation=%s messages=%d\n",
		ev.MemberID, ev.EpochSet.Epoch, ev.Violation, len(ev.Messages))
}

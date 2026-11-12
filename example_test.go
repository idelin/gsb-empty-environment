package netsec_test

import (
	"bytes"
	"errors"
	"fmt"
	"log"

	netsec "example.org/netsec/q0569"
)

// A minimal in-memory "plugin" that performs the v1 greeting and answers a
// wrap-file-key command. Real callers wire the streams to a process started
// with Session.BinaryName.
func ExampleNewSession() {
	pluginOut := bytes.NewBufferString(
		"greet v1\n\n" +
			"recipient-stanza AGE-PLUGIN-TEST-1 0\n" +
			"c2VjcmV0\n" +
			"\n" +
			"done\n\n",
	)
	hostOut := &bytes.Buffer{}

	session, err := netsec.NewSession(pluginOut, hostOut, "AGE-PLUGIN-TEST-1")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(session.PluginName())
	fmt.Println(session.BinaryName())

	session.AddRecipient([]string{"AGE-PLUGIN-TEST-1", "age1recipient"})
	fileKey := make([]byte, netsec.FileKeySize)
	results, err := session.WrapFileKey(fileKey)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(results[0].Type, results[0].Args[0], string(results[0].Body))

	// Output:
	// test
	// age-plugin-test
	// recipient-stanza AGE-PLUGIN-TEST-1 secret
}

func ExampleParsePluginName() {
	name, err := netsec.ParsePluginName("AGE-PLUGIN-yubikey-1")
	if err != nil {
		log.Fatal(err)
	}
	binary, err := netsec.BinaryName(name)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(name, binary)

	if _, err := netsec.ParsePluginName("AGE-PLUGIN-../bin/sh-1"); errors.Is(err, netsec.ErrInvalidPluginName) {
		fmt.Println("injection rejected")
	}

	// Output:
	// yubikey age-plugin-yubikey
	// injection rejected
}

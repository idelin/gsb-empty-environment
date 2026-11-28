// Command mailauth verifies a message file or stdin and prints a JSON report.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"prescreen.local/ps-517b39ba66"
)

func main() {
	ip := flag.String("ip", "", "SMTP client IP address")
	mailFrom := flag.String("mail-from", "", "SMTP MAIL FROM identity")
	helo := flag.String("helo", "", "SMTP HELO/EHLO name")
	flag.Parse()

	if *ip == "" {
		fmt.Fprintln(os.Stderr, "mailauth: -ip is required")
		os.Exit(2)
	}
	parsedIP := net.ParseIP(*ip)
	if parsedIP == nil {
		fmt.Fprintf(os.Stderr, "mailauth: invalid IP %q\n", *ip)
		os.Exit(2)
	}
	var raw []byte
	var err error
	if flag.NArg() > 0 {
		raw, err = os.ReadFile(flag.Arg(0))
	} else {
		raw, err = os.ReadFile("/dev/stdin")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "mailauth: %v\n", err)
		os.Exit(1)
	}

	report, err := mailauth.Verify(context.Background(), raw, mailauth.Options{
		IP:       parsedIP,
		MailFrom: *mailFrom,
		Helo:     *helo,
		Now:      time.Now(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "mailauth: %v\n", err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "mailauth: %v\n", err)
		os.Exit(1)
	}
}

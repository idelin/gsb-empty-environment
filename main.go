package main

import (
	"encoding/json"
	"io"
	"log"
	"os"
)

func run(stdin io.Reader, stdout io.Writer) error {
	var in inputPayload
	decoder := json.NewDecoder(stdin)
	if err := decoder.Decode(&in); err != nil {
		return err
	}

	results := solve(in)
	encoded, err := json.Marshal(results)
	if err != nil {
		return err
	}
	_, err = stdout.Write(append(encoded, '\n'))
	return err
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}

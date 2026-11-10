// Command gen produces a large acldiff input JSON for manual stress runs.
package main

import (
	"encoding/json"
	"math/rand"
	"os"
	"strconv"
)

type input struct {
	Old [][2]string `json:"old"`
	New [][2]string `json:"new"`
}

func main() {
	rng := rand.New(rand.NewSource(20261110))
	in := input{
		Old: make([][2]string, 0, 100000),
		New: make([][2]string, 0, 100000),
	}
	fill := func() [][2]string {
		out := make([][2]string, 0, 100000)
		for i := 0; i < 100000; i++ {
			bits := rng.Intn(33)
			base := rng.Uint32()
			octets := [4]byte{
				byte(base >> 24), byte(base >> 16), byte(base >> 8), byte(base),
			}
			action := "deny"
			if rng.Intn(2) == 0 {
				action = "allow"
			}
			out = append(out, [2]string{
				octetString(octets) + "/" + strconv.Itoa(bits), action,
			})
		}
		return out
	}
	in.Old = fill()
	in.New = fill()
	encoder := json.NewEncoder(os.Stdout)
	if err := encoder.Encode(in); err != nil {
		panic(err)
	}
}

func octetString(o [4]byte) string {
	return strconv.Itoa(int(o[0])) + "." + strconv.Itoa(int(o[1])) + "." +
		strconv.Itoa(int(o[2])) + "." + strconv.Itoa(int(o[3]))
}

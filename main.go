package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
)

type rawInput struct {
	Need   []json.Number     `json:"need"`
	Offers []json.RawMessage `json:"offers"`
}

func toInt64(n json.Number) (int64, error) {
	if v, err := n.Int64(); err == nil {
		return v, nil
	}
	f, ok := new(big.Float).SetString(n.String())
	if !ok {
		return 0, fmt.Errorf("invalid number %q", n.String())
	}
	v, _ := f.Int64()
	return v, nil
}

func numFromRaw(raw json.RawMessage) (int64, error) {
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, err
	}
	return toInt64(n)
}

func parseID(raw json.RawMessage) (ID, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return ID{str: s}, nil
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return ID{}, fmt.Errorf("invalid id %s", string(raw))
	}
	if v, err := toInt64(n); err == nil {
		return ID{isNum: true, num: v}, nil
	}
	return ID{str: n.String()}, nil
}

func (id ID) MarshalJSON() ([]byte, error) {
	if id.isNum {
		return json.Marshal(id.num)
	}
	return json.Marshal(id.str)
}

func parseInput(data []byte) (int64, int64, []offer, error) {
	var in rawInput
	if err := json.Unmarshal(data, &in); err != nil {
		return 0, 0, nil, err
	}
	if len(in.Need) != 2 {
		return 0, 0, nil, fmt.Errorf("need must be [start, end]")
	}
	needL, err := toInt64(in.Need[0])
	if err != nil {
		return 0, 0, nil, fmt.Errorf("need start: %w", err)
	}
	needR, err := toInt64(in.Need[1])
	if err != nil {
		return 0, 0, nil, fmt.Errorf("need end: %w", err)
	}
	offers := make([]offer, 0, len(in.Offers))
	for i, raw := range in.Offers {
		var parts []json.RawMessage
		if err := json.Unmarshal(raw, &parts); err != nil || len(parts) != 4 {
			return 0, 0, nil, fmt.Errorf("offers[%d] must be [id, start, end, cost]", i)
		}
		id, err := parseID(parts[0])
		if err != nil {
			return 0, 0, nil, fmt.Errorf("offers[%d] id: %w", i, err)
		}
		a, err := numFromRaw(parts[1])
		if err != nil {
			return 0, 0, nil, fmt.Errorf("offers[%d] start: %w", i, err)
		}
		b, err := numFromRaw(parts[2])
		if err != nil {
			return 0, 0, nil, fmt.Errorf("offers[%d] end: %w", i, err)
		}
		cost, err := numFromRaw(parts[3])
		if err != nil {
			return 0, 0, nil, fmt.Errorf("offers[%d] cost: %w", i, err)
		}
		if cost < 0 {
			return 0, 0, nil, fmt.Errorf("offers[%d] cost must be non-negative", i)
		}
		offers = append(offers, offer{id: id, a: a, b: b, cost: cost})
	}
	return needL, needR, offers, nil
}

func main() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read stdin:", err)
		os.Exit(1)
	}
	needL, needR, offers, err := parseInput(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse input:", err)
		os.Exit(1)
	}
	res := solve(needL, needR, offers)
	if !res.ok {
		fmt.Println("null")
		return
	}
	ids := res.ids
	if ids == nil {
		ids = []ID{}
	}
	out, err := json.Marshal([]any{res.cost, ids})
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal output:", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}

package bgpsim_test

import (
	"encoding/json"
	"testing"

	bgpsim "prescreen.local/ps-b15ac20600"
)

func TestOriginUnmarshalJSONNamesAndNumbers(t *testing.T) {
	var named struct {
		O bgpsim.Origin `json:"o"`
	}
	if err := json.Unmarshal([]byte(`{"o":"egp"}`), &named); err != nil {
		t.Fatal(err)
	}
	if named.O != bgpsim.OriginEGP {
		t.Fatalf("egp decoded as %d, want 1", named.O)
	}

	var numeric struct {
		O bgpsim.Origin `json:"o"`
	}
	if err := json.Unmarshal([]byte(`{"o":2}`), &numeric); err != nil {
		t.Fatal(err)
	}
	if numeric.O != bgpsim.OriginIncomplete {
		t.Fatalf("2 decoded as %d, want incomplete", numeric.O)
	}

	var bad bgpsim.Origin
	if err := json.Unmarshal([]byte(`"nope"`), &bad); err == nil {
		t.Fatal("invalid origin should error")
	}
}

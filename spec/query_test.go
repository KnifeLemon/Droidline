package spec

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

type queryVectors struct {
	Tree  map[string]any `json:"tree"`
	Cases []struct {
		Name   string         `json:"name"`
		By     string         `json:"by"`
		Value  string         `json:"value"`
		Query  map[string]any `json:"query"`
		Expect [][4]int       `json:"expect"`
	} `json:"cases"`
	Invalid []any `json:"invalid"`
}

func loadQueryVectors(t *testing.T) queryVectors {
	raw, err := os.ReadFile("query-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v queryVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestQueryVectors(t *testing.T) {
	v := loadQueryVectors(t)
	roots := []*Node{NewTree(v.Tree)}
	for _, c := range v.Cases {
		var by any = c.By
		if c.Query != nil {
			q, err := CheckQuery(c.Query)
			if err != nil {
				t.Fatalf("%s: %v", c.Name, err)
			}
			by = q
		}
		var got [][4]int
		for _, n := range FindAll(roots, by, c.Value) {
			got = append(got, n.Bounds())
		}
		if fmt.Sprint(got) != fmt.Sprint(c.Expect) && !(len(got) == 0 && len(c.Expect) == 0) {
			t.Errorf("%s: got %v, want %v", c.Name, got, c.Expect)
		}
	}
}

func TestInvalidQueries(t *testing.T) {
	for _, q := range loadQueryVectors(t).Invalid {
		if _, err := CheckQuery(q); err == nil {
			t.Errorf("CheckQuery(%v) accepted an invalid query", q)
		}
	}
}

func TestQueryKeysMatchSpec(t *testing.T) {
	var fromSpec []string
	for _, k := range MustLoad().Query.Keys {
		fromSpec = append(fromSpec, k.Key)
	}
	if fmt.Sprint(fromSpec) != fmt.Sprint(QueryKeys()) {
		t.Errorf("commands.json query keys %v differ from query.go %v", fromSpec, QueryKeys())
	}
}

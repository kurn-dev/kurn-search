package analyzer_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter/analyzer"
)

func TestNeutralGoldenEquality(t *testing.T) {
	contents, err := os.ReadFile("testdata/neutral-golden.json")
	if err != nil {
		t.Fatal("neutral golden is unavailable")
	}
	var golden struct {
		Schema   string `json:"schema"`
		Fixtures []struct {
			ID    string `json:"id"`
			Text  string `json:"text"`
			Cases []struct {
				View           analyzer.View `json:"view"`
				LocalTerms     []string      `json:"local_terms"`
				OracleSetEqual bool          `json:"oracle_set_equal"`
			} `json:"cases"`
		} `json:"fixtures"`
	}
	if json.Unmarshal(contents, &golden) != nil {
		t.Fatal("neutral golden is malformed")
	}
	if golden.Schema != "kurn-search-analyzer-neutral/v1" || len(golden.Fixtures) != 20 {
		t.Fatal("neutral golden identity or fixture count is not frozen")
	}

	cases := 0
	oracleEqual := 0
	residuals := 0
	for _, fixture := range golden.Fixtures {
		if len(fixture.Cases) != 5 {
			t.Fatalf("fixture %s has %d cases", fixture.ID, len(fixture.Cases))
		}
		for _, goldenCase := range fixture.Cases {
			actual, analyzeErr := analyzer.Analyze(goldenCase.View, fixture.Text)
			if analyzeErr != nil {
				t.Fatalf("%s/%s: %v", fixture.ID, goldenCase.View, analyzeErr)
			}
			assertTerms(t, actual, goldenCase.LocalTerms)
			cases++
			if goldenCase.OracleSetEqual {
				oracleEqual++
			} else {
				residuals++
				if fixture.ID != "astral" || goldenCase.View != analyzer.WordIndex {
					t.Fatalf("unknown oracle residual %s/%s", fixture.ID, goldenCase.View)
				}
			}
		}
	}
	if cases != 100 || oracleEqual != 99 || residuals != 1 {
		t.Fatalf("cases=%d oracle_equal=%d residuals=%d", cases, oracleEqual, residuals)
	}
}

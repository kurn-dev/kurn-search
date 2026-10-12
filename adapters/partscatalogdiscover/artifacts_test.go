package partscatalogdiscover

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
)

func TestFrozenAnswersProjectionAndArtifacts(t *testing.T) {
	f, err := loadFixture()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Records) != 152 || len(f.Queries) != 20 {
		t.Fatal("fixture inventory")
	}
	maximumKeys := 0
	for _, r := range f.Records {
		raw, err := encodeFixtureRecord(r)
		if err != nil {
			t.Fatal(err)
		}
		current, err := New().Project(raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(current) != 1 || current[0].EntryID() != r.ID || !bytes.Contains(current[0].Payload(), []byte(`"name"`)) {
			t.Fatal("projection shape")
		}
		if len(current[0].Keys()) > maximumKeys {
			maximumKeys = len(current[0].Keys())
		}
	}
	if maximumKeys != 38 || len([]rune(f.Records[12].Name)) != 80 || len([]rune(f.Records[13].Name)) == 0 {
		t.Fatalf("fixture bounds: max keys %d", maximumKeys)
	}
	a, err := GenerateArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	for i, pair := range [][2][]byte{{a.SourceProjection, b.SourceProjection}, {a.QueryCodec, b.QueryCodec}, {a.CompiledPlan, b.CompiledPlan}, {a.ResolvedResult, b.ResolvedResult}} {
		if len(pair[0]) == 0 || !bytes.Equal(pair[0], pair[1]) {
			t.Fatalf("component %d", i)
		}
	}
	if !bytes.Contains(a.CompiledPlan, []byte("canonical")) || !bytes.Contains(a.QueryCodec, []byte("within_field")) {
		t.Fatal("missing membership meaning")
	}
	raw := NeutralFixture()
	raw[0] = '!'
	if !bytes.Equal(NeutralFixture(), neutralFixture) {
		t.Fatal("fixture alias")
	}
	if adapter.PlainSHA256(a.SourceProjection) == adapter.PlainSHA256(a.CompiledPlan) {
		t.Fatal("component collision")
	}
}

func TestCheckedExampleMatchesGenerator(t *testing.T) {
	wantSource, err := ExampleSource()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("..", "..", "examples", "parts-catalog-discover")
	gotSource, err := os.ReadFile(filepath.Join(root, "source.ndjson"))
	if err != nil || !bytes.Equal(gotSource, wantSource) {
		t.Fatal("checked discovery source drifted from generator", err)
	}
	gotMapping, err := os.ReadFile(filepath.Join(root, "list-mapping.json"))
	if err != nil || !bytes.Equal(gotMapping, ExampleMapping()) {
		t.Fatal("checked discovery mapping drifted from generator", err)
	}
}

package partscatalogsuggest

import (
	"bytes"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/partscatalog"
)

func TestFrozenAnswersProjectionAndArtifacts(t *testing.T) {
	f, err := loadFixture()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Records) != 152 || len(f.Queries) != 13 {
		t.Fatal("fixture inventory")
	}
	totalKeys := 0
	for _, r := range f.Records {
		raw, err := encodeFixtureRecord(r)
		if err != nil {
			t.Fatal(err)
		}
		old, err := partscatalog.New().Project(raw)
		if err != nil {
			t.Fatal(err)
		}
		current, err := New().Project(raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(old) != 1 || len(current) != 1 || old[0].EntryID() != current[0].EntryID() ||
			len(current[0].Keys()) != 1 || current[0].Keys()[0] != r.Identifier ||
			!bytes.Equal(old[0].Payload(), current[0].Payload()) {
			t.Fatal("suggest projection")
		}
		totalKeys += len(current[0].Keys())
	}
	if totalKeys != 152 {
		t.Fatal("suggest key census")
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
	if !bytes.Contains(a.CompiledPlan, []byte(`"similarity"`)) ||
		!bytes.Contains(a.CompiledPlan, []byte(`"transposition"`)) ||
		!bytes.Contains(a.ResolvedResult, []byte(`"transposition": "yes"`)) ||
		!bytes.Contains(a.CompiledPlan, []byte("canonical")) ||
		!bytes.Contains(a.QueryCodec, []byte("within_field")) ||
		!bytes.Contains(a.ResolvedResult, []byte(`"score"`)) {
		t.Fatal("missing similarity or membership meaning")
	}
	raw := NeutralFixture()
	raw[0] = '!'
	if !bytes.Equal(NeutralFixture(), neutralFixture) {
		t.Fatal("fixture alias")
	}
	old, err := partscatalog.GenerateArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	if adapter.PlainSHA256(a.CompiledPlan) == adapter.PlainSHA256(old.CompiledPlan) {
		t.Fatal("old/new component collision")
	}
}

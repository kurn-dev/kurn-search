package partscatalogselect

import (
	"bytes"
	"slices"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/partscatalog"
)

func TestFrozenAnswersProjectionAndArtifacts(t *testing.T) {
	f, err := loadFixture()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Records) != 152 || len(f.Queries) != 20 {
		t.Fatal("fixture inventory")
	}
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
			!slices.Equal(old[0].Keys(), current[0].Keys()) || !bytes.Equal(old[0].Payload(), current[0].Payload()) {
			t.Fatal("projection drift")
		}
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
	old, err := partscatalog.GenerateArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	if adapter.PlainSHA256(a.CompiledPlan) == adapter.PlainSHA256(old.CompiledPlan) {
		t.Fatal("old/new component collision")
	}
}

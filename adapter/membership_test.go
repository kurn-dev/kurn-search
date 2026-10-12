package adapter_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
)

func member(t *testing.T, name string, values ...string) adapter.MembershipFilterTerm {
	t.Helper()
	term, err := adapter.NewMembershipFilterTerm(name, values)
	if err != nil {
		t.Fatal(err)
	}
	return term
}

func trusted(t *testing.T, name, value string) adapter.ExactFilterTerm {
	t.Helper()
	term, err := adapter.NewExactFilterTerm(name, value)
	if err != nil {
		t.Fatal(err)
	}
	return term
}

func TestMembershipTermNormalizationAndOwnership(t *testing.T) {
	values := []string{"filter", "bearing", "bearing"}
	term, err := adapter.NewMembershipFilterTerm("category", values)
	if err != nil || !term.Valid() || term.Name() != "category" || !reflect.DeepEqual(term.Values(), []string{"bearing", "filter"}) {
		t.Fatal("term normalization failed")
	}
	values[0] = "changed-input"
	got := term.Values()
	got[0] = "changed-output"
	if !reflect.DeepEqual(term.Values(), []string{"bearing", "filter"}) {
		t.Fatal("term membership aliases input or accessor")
	}
	if (adapter.MembershipFilterTerm{}).Valid() {
		t.Fatal("zero term is valid")
	}
}

func TestMembershipTermRefusesBeforeDedupe(t *testing.T) {
	seventeen := make([]string, 17)
	for i := range seventeen {
		seventeen[i] = "active"
	}
	for _, values := range [][]string{nil, {}, {""}, {"x", ""}, {strings.Repeat("x", 513)}, {string([]byte{0xff})}, seventeen} {
		term, err := adapter.NewMembershipFilterTerm("category", values)
		var classified *adapter.Error
		if !errors.As(err, &classified) || classified.Category != adapter.CategoryInvalidContract || term.Valid() {
			t.Fatal("invalid term was not a closed invalid_contract refusal")
		}
	}
}

func TestMembershipPlanProvenanceCanonicalAndCopies(t *testing.T) {
	spec := adapter.MembershipFilterPlanSpec{
		Trusted: []adapter.ExactFilterTerm{trusted(t, "tenant_id", "tenant-a")},
		Caller: []adapter.MembershipFilterTerm{
			member(t, "status", "active"), member(t, "category", "filter", "bearing", "bearing"),
		},
	}
	plan, err := adapter.NewMembershipFilterPlan(spec)
	want := `{"category":{"in":["bearing","filter"]},"status":"active","tenant_id":"tenant-a"}`
	if err != nil || !plan.Valid() || string(plan.CanonicalJSON()) != want {
		t.Fatal("canonical scoped plan failed")
	}
	if plan.Trusted()[0].Name() != "tenant_id" || plan.Caller()[0].Name() != "category" {
		t.Fatal("provenance or canonical name ordering lost")
	}
	spec.Trusted[0] = trusted(t, "tenant_id", "other")
	spec.Caller[1] = member(t, "category", "other")
	gotTrusted, gotCaller, raw := plan.Trusted(), plan.Caller(), plan.CanonicalJSON()
	gotTrusted[0] = adapter.ExactFilterTerm{}
	gotCaller[0] = adapter.MembershipFilterTerm{}
	raw[0] = '!'
	values := plan.Caller()[0].Values()
	values[0] = "other"
	if string(plan.CanonicalJSON()) != want || plan.Trusted()[0].Value() != "tenant-a" || plan.Caller()[0].Values()[0] != "bearing" {
		t.Fatal("plan ownership changed through caller/accessor data")
	}
}

func TestMembershipPlanRejectsInvalidAndCollidingProvenance(t *testing.T) {
	scope := trusted(t, "tenant_id", "tenant-a")
	category := member(t, "category", "bearing")
	tests := []adapter.MembershipFilterPlanSpec{
		{Trusted: []adapter.ExactFilterTerm{{}}},
		{Caller: []adapter.MembershipFilterTerm{{}}},
		{Trusted: []adapter.ExactFilterTerm{scope, scope}},
		{Caller: []adapter.MembershipFilterTerm{category, category}},
		{Trusted: []adapter.ExactFilterTerm{scope}, Caller: []adapter.MembershipFilterTerm{member(t, "tenant_id", "tenant-b")}},
	}
	tooMany := adapter.MembershipFilterPlanSpec{}
	for i := 0; i < 9; i++ {
		tooMany.Caller = append(tooMany.Caller, member(t, fmt.Sprintf("field%d", i), "x"))
	}
	tests = append(tests, tooMany)
	for _, spec := range tests {
		plan, err := adapter.NewMembershipFilterPlan(spec)
		if err == nil || plan.Valid() || plan.CanonicalJSON() != nil {
			t.Fatal("invalid provenance admitted or partial plan returned")
		}
	}
	if (adapter.MembershipFilterPlan{}).Valid() {
		t.Fatal("zero plan admitted")
	}
	empty, err := adapter.NewMembershipFilterPlan(adapter.MembershipFilterPlanSpec{})
	if err != nil || !empty.Valid() || len(empty.CanonicalJSON()) != 0 {
		t.Fatal("constructed empty generic plan should be distinguishable from zero")
	}
}

func TestMembershipPlanCapIncludesTrustedAndPartition(t *testing.T) {
	makeSpec := func(fill int) adapter.MembershipFilterPlanSpec {
		categories, manufacturers := make([]string, 16), make([]string, 16)
		for i := 0; i < 16; i++ {
			categories[i] = fmt.Sprintf("c%02d", i) + strings.Repeat("x", 509)
			manufacturers[i] = fmt.Sprintf("m%02d", i) + strings.Repeat("x", 509)
		}
		manufacturers[15] = "m15" + strings.Repeat("x", fill)
		return adapter.MembershipFilterPlanSpec{
			Trusted: []adapter.ExactFilterTerm{trusted(t, "tenant_id", "tenant-a")},
			Caller:  []adapter.MembershipFilterTerm{member(t, "category", categories...), member(t, "manufacturer", manufacturers...)},
		}
	}
	spec := makeSpec(345)
	plan, err := adapter.NewMembershipFilterPlan(spec)
	if err != nil || len(plan.CanonicalJSON()) != 16384 || adapter.MaxMembershipFilterBytes != 16384 {
		t.Fatal("at-cap scoped plan refused")
	}
	if _, err := adapter.NewMembershipFilterPlan(makeSpec(346)); err == nil {
		t.Fatal("one-over scoped plan admitted")
	}
	spec.Caller = append(spec.Caller, member(t, "partition_id", "north"))
	if _, err := adapter.NewMembershipFilterPlan(spec); err == nil {
		t.Fatal("scope bytes were omitted from cap")
	}
}

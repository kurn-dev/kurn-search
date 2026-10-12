package adapter

import "testing"

func TestExactFilterPlanIsImmutableAndSeparatesProvenance(t *testing.T) {
	tenant, err := NewExactFilterTerm("tenant_id", "tenant-1")
	if err != nil {
		t.Fatal(err)
	}
	partition, err := NewExactFilterTerm("partition_id", "partition-1")
	if err != nil {
		t.Fatal(err)
	}
	trusted := []ExactFilterTerm{tenant}
	caller := []ExactFilterTerm{partition}
	plan, err := NewExactFilterPlan(ExactFilterPlanSpec{Trusted: trusted, Caller: caller})
	if err != nil {
		t.Fatal(err)
	}
	trusted[0] = ExactFilterTerm{}
	caller[0] = ExactFilterTerm{}
	if !plan.Valid() || plan.Trusted()[0].Value() != "tenant-1" || plan.Caller()[0].Value() != "partition-1" {
		t.Fatal("filter plan did not retain immutable provenance")
	}
	copyTrusted := plan.Trusted()
	copyTrusted[0] = ExactFilterTerm{}
	if !plan.Trusted()[0].Valid() {
		t.Fatal("filter plan accessor exposed mutable storage")
	}
}

func TestExactFilterPlanRejectsInvalidTermsAndCollisions(t *testing.T) {
	if _, err := NewExactFilterTerm("", "value"); err == nil {
		t.Fatal("empty name accepted")
	}
	if _, err := NewExactFilterTerm("tenant_id", ""); err == nil {
		t.Fatal("empty value accepted")
	}
	term, _ := NewExactFilterTerm("tenant_id", "tenant-1")
	if _, err := NewExactFilterPlan(ExactFilterPlanSpec{Trusted: []ExactFilterTerm{term}, Caller: []ExactFilterTerm{term}}); err == nil {
		t.Fatal("cross-provenance collision accepted")
	}
	if _, err := NewExactFilterPlan(ExactFilterPlanSpec{Trusted: []ExactFilterTerm{{}}}); err == nil {
		t.Fatal("invalid term accepted")
	}
}

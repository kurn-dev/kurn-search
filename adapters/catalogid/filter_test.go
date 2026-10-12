package catalogid

import "testing"

func TestCompileFilterSeparatesTrustedAndCallerTerms(t *testing.T) {
	semantics := New()
	plan, err := semantics.CompileFilter(
		[]byte(`{"query":"abc","partition_id":"partition-1"}`),
		[]byte(`{"tenant_id":"tenant-1"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	trusted, caller := plan.Trusted(), plan.Caller()
	if len(trusted) != 1 || trusted[0].Name() != "tenant_id" || trusted[0].Value() != "tenant-1" {
		t.Fatalf("unexpected trusted terms: %#v", trusted)
	}
	if len(caller) != 1 || caller[0].Name() != "partition_id" || caller[0].Value() != "partition-1" {
		t.Fatalf("unexpected caller terms: %#v", caller)
	}

	plan, err = semantics.CompileFilter(
		[]byte(`{"query":"abc"}`),
		[]byte(`{"tenant_id":"tenant-1"}`),
	)
	if err != nil || len(plan.Caller()) != 0 {
		t.Fatal("optional partition did not remain absent")
	}
}

func TestCompileFilterReusesStrictDomainParsers(t *testing.T) {
	semantics := New()
	cases := []struct {
		request []byte
		scope   []byte
	}{
		{[]byte(`{"query":"abc","tenant_id":"attacker"}`), []byte(`{"tenant_id":"tenant-1"}`)},
		{[]byte(`{"query":"abc"}`), []byte(`{"tenant_id":""}`)},
		{[]byte(`{"query":"abc"}`), []byte(`{"tenant_id":"tenant-1","tenant_id":"other"}`)},
		{[]byte(`{"query":"abc","partition_id":null}`), []byte(`{"tenant_id":"tenant-1"}`)},
	}
	for index, item := range cases {
		if _, err := semantics.CompileFilter(item.request, item.scope); err == nil {
			t.Fatalf("case %d accepted", index)
		}
	}
}

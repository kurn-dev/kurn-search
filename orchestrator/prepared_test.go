package orchestrator_test

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/kurn-dev/kurn-search/adapters/catalogid"
	"github.com/kurn-dev/kurn-search/orchestrator"
)

func TestSearchPreparedMatchesSearch(t *testing.T) {
	served := stamp("a", 1)
	script := func(call int, request batchRequest) responseSpec {
		if call == 1 {
			return responseSpec{body: success(request, 128, served)}
		}
		return responseSpec{body: success(request, 1, served)}
	}
	direct := newScriptedServer(t, script)
	want, err := newExecutor(t, catalogid.New(), direct.client(t)).Search(context.Background(), searchRequest(), trustedScope())
	if err != nil {
		t.Fatal(err)
	}
	fixture := newScriptedServer(t, script)
	executor := newExecutor(t, catalogid.New(), fixture.client(t))
	request, scope := searchRequest(), trustedScope()
	prepared, err := executor.Prepare(context.Background(), request, scope)
	if err != nil || fixture.calls.Load() != 0 {
		t.Fatalf("prepare: %v, backend calls %d", err, fixture.calls.Load())
	}
	// The prepared value holds its own copies: later caller mutation is inert.
	request[2], scope[2] = 'X', 'X'
	got, err := executor.SearchPrepared(context.Background(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.calls.Load() != direct.calls.Load() || got.Pool() != want.Pool() || got.PoolStep() != want.PoolStep() ||
		got.VersionAttempt() != want.VersionAttempt() || !reflect.DeepEqual(got.ListVersions(), want.ListVersions()) ||
		got.AdapterName() != want.AdapterName() || got.BehaviorSHA256() != want.BehaviorSHA256() ||
		len(got.Resolution().Hits()) != len(want.Resolution().Hits()) {
		t.Fatalf("prepared search differs from Search: %#v vs %#v", got, want)
	}
	for i, hit := range got.Resolution().Hits() {
		if other := want.Resolution().Hits()[i]; hit.EntryID() != other.EntryID() || !bytes.Equal(hit.Result(), other.Result()) {
			t.Fatalf("hit %d differs", i)
		}
	}
}

func TestPrepareRefusesExactlyAsPreflightAndBindsItsExecutor(t *testing.T) {
	fixture := newScriptedServer(t, func(_ int, _ batchRequest) responseSpec {
		t.Fatal("prepare performed backend I/O")
		return responseSpec{}
	})
	executor := newExecutor(t, catalogid.New(), fixture.client(t))
	other := newExecutor(t, catalogid.New(), fixture.client(t))
	badScope := []byte(`{"tenant_id":"a","tenant_id":"b"}`)
	_, prepareErr := executor.Prepare(context.Background(), searchRequest(), badScope)
	preflightErr := executor.Preflight(context.Background(), searchRequest(), badScope)
	if category(t, prepareErr).Category != category(t, preflightErr).Category {
		t.Fatalf("prepare %v and preflight %v differ", prepareErr, preflightErr)
	}
	prepared, err := executor.Prepare(context.Background(), searchRequest(), trustedScope())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.SearchPrepared(context.Background(), prepared); category(t, err).Category != orchestrator.CategoryInvalidInput {
		t.Fatalf("a prepared request ran on another executor: %v", err)
	}
	if _, err := executor.SearchPrepared(context.Background(), orchestrator.Prepared{}); category(t, err).Category != orchestrator.CategoryInvalidInput {
		t.Fatalf("a zero prepared request ran: %v", err)
	}
	if fixture.calls.Load() != 0 {
		t.Fatal("refused prepared searches reached the backend")
	}
}

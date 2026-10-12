package orchestrator

import (
	"github.com/kurn-dev/kurn-search/adapter"
)

type ListVersion struct {
	role  adapter.ListRole
	name  string
	stamp string
}

func (v ListVersion) Role() adapter.ListRole { return v.role }
func (v ListVersion) Name() string           { return v.name }
func (v ListVersion) Stamp() string          { return v.stamp }

type Result struct {
	resolution         adapter.Resolution
	summary            adapter.Summary
	alternativeSummary adapter.AlternativeSummary
	versions           []ListVersion
	adapterName        string
	adapterVersion     string
	behaviorSHA256     string
	versionAttempt     int
	poolStep           int
	pool               int
	backendTookUS      int64
	fallbackTaken      bool
	fallbackHits       int
	fallbackCalls      int
	valid              bool
}

// FallbackTaken reports whether the adapter's fallback step ran in the
// producing rung, and FallbackHits how many candidates it observed there.
// FallbackCalls counts every physical fallback batch sent during the whole
// search, including rungs and attempts that were escalated or restarted.
func (r Result) FallbackTaken() bool { return r.fallbackTaken }
func (r Result) FallbackHits() int   { return r.fallbackHits }
func (r Result) FallbackCalls() int  { return r.fallbackCalls }

func (r Result) Resolution() adapter.Resolution { return r.resolution }

// Summary returns the matched-set summary. Its Valid method is the explicit
// presence discriminator: only facet-constructed executors produce a valid
// summary, and they always do on success.
func (r Result) Summary() adapter.Summary                       { return r.summary }
func (r Result) AlternativeSummary() adapter.AlternativeSummary { return r.alternativeSummary }
func (r Result) ListVersions() []ListVersion                    { return append([]ListVersion(nil), r.versions...) }
func (r Result) AdapterName() string                            { return r.adapterName }
func (r Result) AdapterVersion() string                         { return r.adapterVersion }
func (r Result) BehaviorSHA256() string                         { return r.behaviorSHA256 }
func (r Result) VersionAttempt() int                            { return r.versionAttempt }
func (r Result) PoolStep() int                                  { return r.poolStep }
func (r Result) Pool() int                                      { return r.pool }
func (r Result) BackendTookUS() int64                           { return r.backendTookUS }
func (r Result) Valid() bool                                    { return r.valid }

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"slices"
	"time"
)

func (s *Service) HTTPServer(address string) (*http.Server, error) {
	if s == nil || address == "" || s.maxTimeout <= 0 || s.maxTimeout > 600*time.Second {
		return nil, errInvalidService
	}
	return &http.Server{
		Addr: address, Handler: s,
		ReadHeaderTimeout: ReadHeaderTimeout,
		ReadTimeout:       ReadTimeout,
		WriteTimeout:      s.maxTimeout + writeGrace,
		IdleTimeout:       IdleTimeout,
		MaxHeaderBytes:    MaxHeaderBytes,
		ErrorLog:          log.New(io.Discard, "", 0),
	}, nil
}

func Shutdown(ctx context.Context, server *http.Server) error {
	if ctx == nil || server == nil {
		return errInvalidService
	}
	operation, cancel := context.WithTimeout(ctx, ShutdownTimeout)
	defer cancel()
	return server.Shutdown(operation)
}

// Ready runs every configured golden through authentication, HTTP parsing,
// admission, live execution, resolution, and response validation.
func (s *Service) Ready(ctx context.Context) error {
	if s == nil || ctx == nil {
		return errInvalidService
	}
	for _, golden := range s.goldens {
		body := map[string]any{"query": golden.Query, "limit": golden.Limit}
		if golden.PartitionID != "" {
			body["partition_id"] = golden.PartitionID
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return errInvalidService
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost,
			"http://readiness.invalid/v1/search/"+golden.Route, bytes.NewReader(raw))
		if err != nil {
			return errInvalidService
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+golden.AuthHandle)
		principal, err := s.authenticator.Authenticate(ctx, request.Header.Clone())
		if err != nil || !principal.Valid() || principal.Tenant() != golden.SentinelTenant {
			return errInvalidService
		}
		writer := newCaptureWriter()
		s.ServeHTTP(writer, request)
		if writer.status != http.StatusOK {
			return errInvalidService
		}
		ids, err := readinessHitIDs(writer.body.Bytes(), golden.ExpectedComplete)
		if err != nil {
			return errInvalidService
		}
		if !slices.Equal(ids, golden.ExpectedIDs) {
			return errInvalidService
		}
	}
	return nil
}

// Readiness asserts only its route-independent golden. Route-specific
// summary shapes are checked by their own encoders and client contract;
// decoding the alternatives object into the older facet arrays would reject
// a healthy route before serving.
func readinessHitIDs(raw []byte, expectedComplete bool) ([]string, error) {
	var response struct {
		Complete bool          `json:"complete"`
		Hits     []hitResponse `json:"hits"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&response); err != nil || response.Complete != expectedComplete {
		return nil, errInvalidService
	}
	ids := make([]string, len(response.Hits))
	for index, hit := range response.Hits {
		ids[index] = hit.EntryID
	}
	return ids, nil
}

type captureWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newCaptureWriter() *captureWriter       { return &captureWriter{header: make(http.Header)} }
func (w *captureWriter) Header() http.Header { return w.header }
func (w *captureWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *captureWriter) Write(value []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(value)
}

var _ http.ResponseWriter = (*captureWriter)(nil)

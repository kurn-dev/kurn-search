package kurnhttp

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"io"
	"sync"
	"unicode/utf8"
)

// valueDecoders pools single-pass decoders for object and array: every kurnd
// response, result, version map and candidate goes through them, and a fresh
// decoder per call dominated the client's allocation profile.
var valueDecoders = sync.Pool{New: func() any {
	p := new(pooledDecoder)
	p.decoder = jsontext.NewDecoder(&p.reader)
	return p
}}

type pooledDecoder struct {
	reader  bytes.Reader
	decoder *jsontext.Decoder
	size    int
}

// pooledInputLimit bounds what the pool may retain: a decoder keeps buffer
// capacity sized to its largest input, so one that decoded a large response
// is dropped for the collector instead of being pooled.
const pooledInputLimit = 64 << 10

// release drops the decoder's reference to its input and pools it only if
// that input was small.
func (p *pooledDecoder) release() {
	p.reader.Reset(nil)
	p.decoder.Reset(&p.reader)
	if p.size <= pooledInputLimit {
		valueDecoders.Put(p)
	}
}

// open starts a pooled decoder on raw with encoding/json's own options:
// duplicate names are checked by the callers at the top level only (nested
// values are kept raw, as before) and string escapes unescape as before.
func open(raw []byte) *pooledDecoder {
	p := valueDecoders.Get().(*pooledDecoder)
	p.size = len(raw)
	p.reader.Reset(raw)
	p.decoder.Reset(&p.reader, jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
	return p
}

// close checks the end delimiter and that nothing but whitespace follows, then
// returns the decoder to the pool.
func (p *pooledDecoder) close(end jsontext.Kind) error {
	defer p.release()
	if token, err := p.decoder.ReadToken(); err != nil || token.Kind() != end {
		return io.ErrUnexpectedEOF
	}
	if _, err := p.decoder.ReadToken(); !errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func object(raw []byte) (map[string]json.RawMessage, error) {
	if !utf8.Valid(raw) {
		return nil, io.ErrUnexpectedEOF
	}
	p := open(raw)
	if token, err := p.decoder.ReadToken(); err != nil || token.Kind() != '{' {
		p.release()
		return nil, io.ErrUnexpectedEOF
	}
	out := make(map[string]json.RawMessage)
	for p.decoder.PeekKind() != '}' {
		name, err := p.decoder.ReadToken()
		if err != nil || name.Kind() != '"' {
			p.release()
			return nil, io.ErrUnexpectedEOF
		}
		key := name.String()
		if _, duplicate := out[key]; duplicate {
			p.release()
			return nil, io.ErrUnexpectedEOF
		}
		value, err := p.decoder.ReadValue()
		if err != nil {
			p.release()
			return nil, io.ErrUnexpectedEOF
		}
		out[key] = bytes.Clone(value)
	}
	if err := p.close('}'); err != nil {
		return nil, err
	}
	return out, nil
}

func array(raw []byte) ([]json.RawMessage, error) {
	if !utf8.Valid(raw) {
		return nil, io.ErrUnexpectedEOF
	}
	p := open(raw)
	if token, err := p.decoder.ReadToken(); err != nil || token.Kind() != '[' {
		p.release()
		return nil, io.ErrUnexpectedEOF
	}
	out := make([]json.RawMessage, 0)
	for p.decoder.PeekKind() != ']' {
		value, err := p.decoder.ReadValue()
		if err != nil {
			p.release()
			return nil, io.ErrUnexpectedEOF
		}
		out = append(out, bytes.Clone(value))
	}
	if err := p.close(']'); err != nil {
		return nil, err
	}
	return out, nil
}

func required[T any](m map[string]json.RawMessage, key string, dst *T) error {
	raw, ok := m[key]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return io.ErrUnexpectedEOF
	}
	// Unmarshal refuses trailing data exactly as the former Decode plus EOF
	// token check did.
	return json.Unmarshal(raw, dst)
}

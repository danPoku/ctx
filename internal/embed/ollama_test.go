// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fixedVector(n int, v float32) []float32 {
	vec := make([]float32, n)
	for i := range vec {
		vec[i] = v
	}
	return vec
}

func TestOllamaClientEmbedSuccess(t *testing.T) {
	var gotBody embedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("path = %q, want /api/embed", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		json.NewEncoder(w).Encode(embedResponse{Embeddings: [][]float32{fixedVector(Dims, 0.5)}})
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL, "nomic-embed-text")
	vec, err := c.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != Dims {
		t.Fatalf("len(vec) = %d, want %d", len(vec), Dims)
	}
	if vec[0] != 0.5 {
		t.Errorf("vec[0] = %v, want 0.5", vec[0])
	}
	if gotBody.Model != "nomic-embed-text" || gotBody.Input != "hello world" {
		t.Errorf("request body = %+v, want model=nomic-embed-text input=%q", gotBody, "hello world")
	}
}

func TestOllamaClientWrongDimsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(embedResponse{Embeddings: [][]float32{fixedVector(384, 0.1)}})
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL, "wrong-model")
	_, err := c.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("Embed with a 384-dim response = nil error, want one (mismatched with the 768-dim chunk_vectors column)")
	}
	if !strings.Contains(err.Error(), "384") {
		t.Errorf("error = %q, want it to mention the actual dimension returned", err)
	}
}

func TestOllamaClientHTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(embedResponse{Error: "model 'nomic-embed-text' not found"})
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL, "nomic-embed-text")
	_, err := c.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("Embed against a 404 response = nil error, want one")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want it to surface the server's error message", err)
	}
}

func TestOllamaClientConnectionRefused(t *testing.T) {
	// No server listening on this port at all.
	c := NewOllamaClient("http://127.0.0.1:1", "nomic-embed-text")
	_, err := c.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("Embed against an unreachable server = nil error, want one")
	}
	if !strings.Contains(err.Error(), "ollama serve") {
		t.Errorf("error = %q, want a hint to check `ollama serve`", err)
	}
}

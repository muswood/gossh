// owner: muswood | Email: mumu920@outlook.com
package rag

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestQdrantBackendLifecycle(t *testing.T) {
	collectionCreated := false
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("api-key") != "secret" {
			t.Fatalf("missing Qdrant API key")
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/collections/docs") {
			if !collectionCreated {
				return response(http.StatusNotFound, `missing`), nil
			}
			return response(http.StatusOK, `{"result":{"config":{"params":{"vectors":{"size":2}}}}}`), nil
		}
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/collections/docs") {
			collectionCreated = true
			return response(http.StatusOK, `{}`), nil
		}
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/points") {
			var payload struct {
				Points []struct {
					ID string `json:"id"`
				} `json:"points"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.Points) != 1 || payload.Points[0].ID != qdrantPointID("doc-1") {
				t.Fatalf("unexpected upsert payload: %#v %v", payload, err)
			}
			return response(http.StatusOK, `{}`), nil
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/points/search") {
			return response(http.StatusOK, `{"result":[{"id":"`+qdrantPointID("doc-1")+`","score":0.91,"payload":{"_gossh_id":"doc-1"}}]}`), nil
		}
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/points/delete") {
			return response(http.StatusOK, `{}`), nil
		}
		return response(http.StatusNotFound, `not found`), nil
	})

	backend, err := NewQdrantBackend(QdrantConfig{Endpoint: "http://qdrant.test", Collection: "docs", APIKey: "secret", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	doc := Document{ID: "doc-1", Title: "SSH", Content: "certificate", Embedding: []float32{1, 0}, EmbeddingVersion: "v1"}
	if err := backend.Upsert(context.Background(), []Document{doc}); err != nil {
		t.Fatal(err)
	}
	hits, err := backend.Search(context.Background(), []float32{1, 0}, 5)
	if err != nil || len(hits) != 1 || hits[0].ID != "doc-1" || hits[0].Score != 0.91 {
		t.Fatalf("unexpected vector hits: %#v %v", hits, err)
	}
	if err := backend.Delete(context.Background(), []string{"doc-1"}); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

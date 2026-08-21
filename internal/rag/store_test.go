// owner: muswood | Email: mumu920@outlook.com
package rag

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type reverseReranker struct{}

func (reverseReranker) Rerank(_ context.Context, _ string, results []SearchResult) ([]SearchResult, error) {
	for i, j := 0, len(results)-1; i < j; i, j = i+1, j-1 {
		results[i], results[j] = results[j], results[i]
	}
	return results, nil
}

type testEmbedder struct{ version string }

func (e testEmbedder) Version() string { return e.version }
func (e testEmbedder) Embed(_ context.Context, value string) ([]float32, error) {
	return []float32{float32(len(value)), 1}, nil
}

type failingEmbedder struct{}

func (failingEmbedder) Version() string { return "failing-v1" }
func (failingEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, errors.New("embedding provider unavailable")
}

type batchTestEmbedder struct {
	version string
	calls   int
}

func (e *batchTestEmbedder) Version() string { return e.version }
func (e *batchTestEmbedder) Embed(_ context.Context, value string) ([]float32, error) {
	return []float32{float32(len(value)), 1}, nil
}
func (e *batchTestEmbedder) EmbedBatch(_ context.Context, values []string) ([][]float32, error) {
	e.calls++
	result := make([][]float32, len(values))
	for i, value := range values {
		result[i] = []float32{float32(len(value)), 1}
	}
	return result, nil
}

func TestStoreAddSearchListDelete(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "rag.json"))
	doc, err := store.Add("SSH 登录失败", "Permission denied publickey usually means the key, certificate, or principal is wrong.")
	if err != nil {
		t.Fatal(err)
	}
	results, err := store.Search("certificate principal", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != doc.ID {
		t.Fatalf("unexpected search results: %+v", results)
	}
	if results[0].Rank != 1 || len(results[0].MatchedTerms) == 0 || results[0].Version == "" {
		t.Fatalf("search result lacks retrieval metadata: %+v", results[0])
	}
	docs, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Content != "" {
		t.Fatalf("list should hide content: %+v", docs)
	}
	if err := store.Delete(doc.ID); err != nil {
		t.Fatal(err)
	}
	results, err = store.Search("certificate", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("deleted doc still searchable: %+v", results)
	}
}

func TestSearchFallsBackToLexicalWhenEmbeddingFails(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "rag.json"))
	doc, err := store.Add("SSH", "certificate principal")
	if err != nil {
		t.Fatal(err)
	}
	store.SetEmbedder(failingEmbedder{})
	results, err := store.Search("certificate", 3)
	if err != nil {
		t.Fatalf("lexical fallback returned error: %v", err)
	}
	if len(results) != 1 || results[0].ID != doc.ID {
		t.Fatalf("unexpected lexical fallback results: %#v", results)
	}
}

func TestSearchWithOptionsAppliesACLAndReranker(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "rag.json"))
	public, err := store.Add("public", "certificate troubleshooting")
	if err != nil {
		t.Fatal(err)
	}
	private, err := store.AddWithAccessMetadata("private", "certificate certificate principal", "internal", nil, []string{"ops"})
	if err != nil {
		t.Fatal(err)
	}
	results, err := store.SearchWithOptions(context.Background(), "certificate", 10, SearchOptions{Scopes: []string{"ops"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || (results[0].ID != private.ID && results[0].ID != public.ID) {
		t.Fatalf("unexpected scoped results: %#v", results)
	}
	lexicalFirst := results[0].ID
	results, err = store.SearchWithOptions(context.Background(), "certificate", 10, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.ID == private.ID {
			t.Fatal("scoped document leaked into public search")
		}
	}
	results, err = store.SearchWithOptions(context.Background(), "certificate", 10, SearchOptions{Scopes: []string{"ops"}, Reranker: reverseReranker{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].ID == lexicalFirst {
		t.Fatalf("reranker order was not preserved: %#v", results)
	}
}

func TestEmbeddingIndexIsPersistedAndReindexIsIncremental(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rag.json")
	store := NewStoreWithEmbedder(path, testEmbedder{version: "v1"})
	if _, err := store.Add("title", "content"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		t.Fatalf("embedding index was not persisted: %v", err)
	}
	upgraded := NewStoreWithEmbedder(path, testEmbedder{version: "v2"})
	changed, err := upgraded.Reindex(context.Background())
	if err != nil || changed != 1 {
		t.Fatalf("expected one incremental reindex, changed=%d err=%v", changed, err)
	}
	changed, err = upgraded.Reindex(context.Background())
	if err != nil || changed != 0 {
		t.Fatalf("second reindex should be a no-op, changed=%d err=%v", changed, err)
	}
}

func TestSQLiteStoreImportsLegacyJSONAndPersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "rag.json")
	legacy := NewStore(legacyPath)
	if _, err := legacy.Add("legacy", "certificate principal"); err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLiteStore(filepath.Join(dir, "rag.db"), testEmbedder{version: "sqlite-v1"})
	if err != nil {
		t.Fatal(err)
	}
	if imported, err := store.ImportJSON(legacyPath); err != nil || imported != 1 {
		t.Fatalf("legacy import = %d, err=%v", imported, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSQLiteStore(filepath.Join(dir, "rag.db"), testEmbedder{version: "sqlite-v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	results, err := reopened.Search("certificate", 5)
	if err != nil || len(results) != 1 || results[0].Title != "legacy" {
		t.Fatalf("SQLite RAG data was not preserved: %#v %v", results, err)
	}
}

func TestReindexUsesBatchEmbedder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rag.json")
	store := NewStore(path)
	if _, err := store.Add("one", "first document"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add("two", "second document"); err != nil {
		t.Fatal(err)
	}
	embedder := &batchTestEmbedder{version: "batch-v2"}
	store.SetEmbedder(embedder)
	changed, err := store.Reindex(context.Background())
	if err != nil || changed != 2 || embedder.calls != 1 {
		t.Fatalf("reindex did not batch embeddings: changed=%d calls=%d err=%v", changed, embedder.calls, err)
	}
}

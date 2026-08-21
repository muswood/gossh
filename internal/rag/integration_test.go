// owner: muswood | Email: mumu920@outlook.com
package rag

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveQdrantIntegration is opt-in because it writes and removes one
// uniquely named point in a real Qdrant collection.
func TestLiveQdrantIntegration(t *testing.T) {
	if os.Getenv("GOSSH_QDRANT_INTEGRATION") != "1" {
		t.Skip("set GOSSH_QDRANT_INTEGRATION=1 to run against Qdrant")
	}
	endpoint := requiredQdrantEnv(t, "GOSSH_QDRANT_ENDPOINT")
	collection := requiredQdrantEnv(t, "GOSSH_QDRANT_COLLECTION")
	backend, err := NewQdrantBackend(QdrantConfig{Endpoint: endpoint, Collection: collection, APIKey: os.Getenv("GOSSH_QDRANT_API_KEY")})
	if err != nil {
		t.Fatal(err)
	}

	documentID := fmt.Sprintf("gossh-integration-%d", time.Now().UnixNano())
	doc := Document{ID: documentID, Title: "GoSSH integration", Content: "Qdrant round trip", Source: "integration-test", Embedding: []float32{1, 0}, EmbeddingVersion: "integration-v1"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := backend.Upsert(ctx, []Document{doc}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = backend.Delete(cleanupCtx, []string{documentID})
	})
	hits, err := backend.Search(ctx, []float32{1, 0}, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range hits {
		if hit.ID == documentID {
			return
		}
	}
	t.Fatalf("Qdrant search did not return upserted document %q: %#v", documentID, hits)
}

func requiredQdrantEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required when GOSSH_QDRANT_INTEGRATION=1", name)
	}
	return value
}

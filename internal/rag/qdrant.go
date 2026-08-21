// owner: muswood | Email: mumu920@outlook.com
package rag

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// QdrantConfig configures Qdrant's documented REST API. The endpoint can be
// a self-hosted instance or a managed Qdrant cluster base URL.
type QdrantConfig struct {
	Endpoint   string
	Collection string
	APIKey     string
	HTTPClient *http.Client
}

// QdrantBackend stores ANN vectors remotely while Store retains document
// metadata and access checks locally.
type QdrantBackend struct {
	endpoint   string
	collection string
	apiKey     string
	http       *http.Client
	mu         sync.Mutex
	dimensions int
}

func NewQdrantBackend(cfg QdrantConfig) (*QdrantBackend, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("Qdrant endpoint 必须是 http 或 https URL")
	}
	collection := strings.TrimSpace(cfg.Collection)
	if collection == "" {
		return nil, fmt.Errorf("Qdrant collection 不能为空")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &QdrantBackend{endpoint: endpoint, collection: collection, apiKey: cfg.APIKey, http: client}, nil
}

func (b *QdrantBackend) Upsert(ctx context.Context, docs []Document) error {
	if len(docs) == 0 {
		return nil
	}
	dimensions := len(docs[0].Embedding)
	if dimensions == 0 {
		return fmt.Errorf("Qdrant 文档缺少向量")
	}
	for _, doc := range docs {
		if len(doc.Embedding) != dimensions {
			return fmt.Errorf("Qdrant 向量维度不一致")
		}
	}
	if err := b.ensureCollection(ctx, dimensions); err != nil {
		return err
	}
	type point struct {
		ID      string    `json:"id"`
		Vector  []float32 `json:"vector"`
		Payload any       `json:"payload"`
	}
	points := make([]point, 0, len(docs))
	for _, doc := range docs {
		points = append(points, point{ID: qdrantPointID(doc.ID), Vector: doc.Embedding, Payload: map[string]any{
			"_gossh_id": doc.ID,
			"title":     doc.Title, "source": doc.Source, "tags": doc.Tags, "scopes": doc.Scopes,
			"version": doc.Version, "embeddingVersion": doc.EmbeddingVersion,
		}})
	}
	return b.request(ctx, http.MethodPut, "/collections/"+url.PathEscape(b.collection)+"/points?wait=true", map[string]any{"points": points}, nil)
}

func (b *QdrantBackend) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	pointIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		pointIDs = append(pointIDs, qdrantPointID(id))
	}
	return b.request(ctx, http.MethodPost, "/collections/"+url.PathEscape(b.collection)+"/points/delete?wait=true", map[string]any{"points": pointIDs}, nil)
}

func (b *QdrantBackend) Search(ctx context.Context, vector []float32, limit int) ([]VectorHit, error) {
	if len(vector) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}
	var response struct {
		Result []struct {
			ID      json.RawMessage            `json:"id"`
			Score   float64                    `json:"score"`
			Payload map[string]json.RawMessage `json:"payload"`
		} `json:"result"`
	}
	if err := b.request(ctx, http.MethodPost, "/collections/"+url.PathEscape(b.collection)+"/points/search", map[string]any{
		"vector": vector, "limit": limit, "with_payload": true,
	}, &response); err != nil {
		return nil, err
	}
	hits := make([]VectorHit, 0, len(response.Result))
	for _, item := range response.Result {
		var id string
		if rawID, ok := item.Payload["_gossh_id"]; ok {
			if err := json.Unmarshal(rawID, &id); err != nil {
				return nil, fmt.Errorf("解析 Qdrant 原始文档 ID 失败: %w", err)
			}
		} else if err := json.Unmarshal(item.ID, &id); err != nil {
			return nil, fmt.Errorf("解析 Qdrant point id 失败: %w", err)
		}
		hits = append(hits, VectorHit{ID: id, Score: item.Score})
	}
	return hits, nil
}

func (b *QdrantBackend) ensureCollection(ctx context.Context, dimensions int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.dimensions == dimensions {
		return nil
	}
	path := "/collections/" + url.PathEscape(b.collection)
	var existing struct {
		Result struct {
			Config struct {
				Params struct {
					Vectors struct {
						Size int `json:"size"`
					} `json:"vectors"`
				} `json:"params"`
			} `json:"config"`
		} `json:"result"`
	}
	err := b.request(ctx, http.MethodGet, path, nil, &existing)
	if err == nil {
		if existing.Result.Config.Params.Vectors.Size != dimensions {
			return fmt.Errorf("Qdrant collection %s 向量维度为 %d，当前 Embedding 为 %d；请新建 collection 或重建索引", b.collection, existing.Result.Config.Params.Vectors.Size, dimensions)
		}
		b.dimensions = dimensions
		return nil
	}
	if !strings.Contains(err.Error(), "HTTP 404") {
		return err
	}
	if err := b.request(ctx, http.MethodPut, path, map[string]any{"vectors": map[string]any{"size": dimensions, "distance": "Cosine"}}, nil); err != nil {
		return err
	}
	b.dimensions = dimensions
	return nil
}

func qdrantPointID(documentID string) string {
	digest := sha256.Sum256([]byte("gossh-rag:" + documentID))
	digest[6] = (digest[6] & 0x0f) | 0x40
	digest[8] = (digest[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(digest[:16])
	return hexValue[0:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:32]
}

func (b *QdrantBackend) request(ctx context.Context, method, path string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.endpoint+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if b.apiKey != "" {
		req.Header.Set("api-key", b.apiKey)
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("Qdrant 请求失败: %w", err)
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Qdrant HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("解析 Qdrant 响应失败: %w", err)
		}
	}
	return nil
}

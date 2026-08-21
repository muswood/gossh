// owner: muswood | Email: mumu920@outlook.com
package rag

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

import _ "modernc.org/sqlite"

const maxDocumentBytes = 512 << 10

type Document struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	Content          string    `json:"content,omitempty"`
	Source           string    `json:"source,omitempty"`
	Tags             []string  `json:"tags,omitempty"`
	Scopes           []string  `json:"scopes,omitempty"`
	Version          string    `json:"version,omitempty"`
	EmbeddingVersion string    `json:"embeddingVersion,omitempty"`
	Embedding        []float32 `json:"embedding,omitempty"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type SearchResult struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Snippet      string    `json:"snippet"`
	Score        float64   `json:"score"`
	VectorScore  float64   `json:"vectorScore,omitempty"`
	MatchedTerms []string  `json:"matchedTerms,omitempty"`
	Source       string    `json:"source,omitempty"`
	Version      string    `json:"version,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt,omitempty"`
	Rank         int       `json:"rank,omitempty"`
}

type SearchOptions struct {
	Scopes   []string
	Reranker Reranker
}

type Embedder interface {
	Embed(context.Context, string) ([]float32, error)
	Version() string
}

// BatchEmbedder lets remote providers amortize HTTP and model overhead during
// reindexing. Stores fall back to Embed when a provider only supports one item.
type BatchEmbedder interface {
	EmbedBatch(context.Context, []string) ([][]float32, error)
}

type FunctionEmbedder struct {
	VersionValue string
	Fn           func(context.Context, string) ([]float32, error)
	BatchFn      func(context.Context, []string) ([][]float32, error)
}

func (e FunctionEmbedder) Version() string {
	if e.VersionValue == "" {
		return "remote-v1"
	}
	return e.VersionValue
}

func (e FunctionEmbedder) Embed(ctx context.Context, value string) ([]float32, error) {
	if e.Fn == nil {
		return nil, fmt.Errorf("Embedding 函数未配置")
	}
	return e.Fn(ctx, value)
}

func (e FunctionEmbedder) EmbedBatch(ctx context.Context, values []string) ([][]float32, error) {
	if e.BatchFn != nil {
		return e.BatchFn(ctx, values)
	}
	result := make([][]float32, len(values))
	for i, value := range values {
		vector, err := e.Embed(ctx, value)
		if err != nil {
			return nil, err
		}
		result[i] = vector
	}
	return result, nil
}

const defaultEmbeddingVersion = "local-hash-v1"

// hashEmbedder is a deterministic local fallback. Deployments can replace it
// with a model-backed Embedder without changing document or search APIs.
type hashEmbedder struct{ dimensions int }

func (e hashEmbedder) Version() string { return defaultEmbeddingVersion }

func (e hashEmbedder) Embed(_ context.Context, value string) ([]float32, error) {
	if e.dimensions <= 0 {
		e.dimensions = 128
	}
	vector := make([]float32, e.dimensions)
	for _, term := range terms(value) {
		digest := sha256.Sum256([]byte(term))
		index := int(binary.BigEndian.Uint32(digest[:4]) % uint32(e.dimensions))
		sign := float32(1)
		if digest[4]&1 == 1 {
			sign = -1
		}
		vector[index] += sign
	}
	return normalize(vector), nil
}

// Reranker is intentionally an extension point for a cross-encoder or model
// service. The lexical index remains the deterministic fallback.
type Reranker interface {
	Rerank(context.Context, string, []SearchResult) ([]SearchResult, error)
}

// VectorBackend is the production ANN extension point. Store keeps document
// metadata locally so ACLs and offline recovery remain available when the
// remote index is unavailable or disabled.
type VectorBackend interface {
	Upsert(context.Context, []Document) error
	Delete(context.Context, []string) error
	Search(context.Context, []float32, int) ([]VectorHit, error)
}

type VectorHit struct {
	ID    string
	Score float64
}

type Store struct {
	path     string
	mu       sync.Mutex
	embedder Embedder
	backend  VectorBackend
	db       *sql.DB
}

// NewSQLiteStore creates the durable RAG backend used by the application.
// SQLite WAL mode provides atomic updates and safe concurrent readers without
// requiring a separate service for a desktop deployment.
func NewSQLiteStore(path string, embedder Embedder) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	store := NewStoreWithEmbedder(path, embedder)
	store.db = db
	for _, query := range []string{
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS rag_documents (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT '',
			tags TEXT NOT NULL DEFAULT '[]',
			scopes TEXT NOT NULL DEFAULT '[]',
			version TEXT NOT NULL DEFAULT '',
			embedding_version TEXT NOT NULL DEFAULT '',
			embedding TEXT NOT NULL DEFAULT '[]',
			updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_rag_documents_updated ON rag_documents(updated_at DESC)`,
	} {
		if _, err := db.Exec(query); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("初始化 SQLite RAG 存储失败: %w", err)
		}
	}
	return store, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// ImportJSON imports the legacy JSON index only when the SQLite store is
// empty. The source file remains untouched for rollback compatibility.
func (s *Store) ImportJSON(path string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return 0, fmt.Errorf("RAG SQLite 存储未初始化")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM rag_documents`).Scan(&count); err != nil {
		return 0, err
	}
	if count > 0 {
		return 0, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var docs []Document
	if err := json.Unmarshal(data, &docs); err != nil {
		return 0, fmt.Errorf("读取旧 RAG 索引失败: %w", err)
	}
	if err := s.ensureEmbeddingsLocked(context.Background(), docs); err != nil {
		return 0, err
	}
	if err := s.saveLocked(docs); err != nil {
		return 0, err
	}
	return len(docs), nil
}

func NewStore(path string) *Store {
	return NewStoreWithEmbedder(path, hashEmbedder{dimensions: 128})
}

func NewStoreWithEmbedder(path string, embedder Embedder) *Store {
	if embedder == nil {
		embedder = hashEmbedder{dimensions: 128}
	}
	return &Store{path: path, embedder: embedder}
}

func (s *Store) SetEmbedder(embedder Embedder) {
	if s == nil || embedder == nil {
		return
	}
	s.mu.Lock()
	s.embedder = embedder
	s.mu.Unlock()
}

func (s *Store) SetVectorBackend(backend VectorBackend) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.backend = backend
	s.mu.Unlock()
}

func (s *Store) Add(title, content string) (*Document, error) {
	return s.AddWithMetadata(title, content, "", nil)
}

func (s *Store) AddWithMetadata(title, content, source string, tags []string) (*Document, error) {
	return s.AddWithAccessMetadata(title, content, source, tags, nil)
}

func (s *Store) AddWithAccessMetadata(title, content, source string, tags, scopes []string) (*Document, error) {
	title = strings.TrimSpace(title)
	content = strings.TrimSpace(content)
	if title == "" || content == "" {
		return nil, fmt.Errorf("知识库文档标题和内容不能为空")
	}
	if len([]byte(content)) > maxDocumentBytes {
		return nil, fmt.Errorf("知识库文档超过 %d KB", maxDocumentBytes>>10)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	docs, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	doc := Document{ID: newID(), Title: title, Content: content, Source: strings.TrimSpace(source), Tags: append([]string(nil), tags...), Scopes: append([]string(nil), scopes...), Version: contentVersion(title, content), UpdatedAt: time.Now()}
	if err := s.ensureEmbeddingLocked(context.Background(), &doc); err != nil {
		return nil, err
	}
	docs = append(docs, doc)
	if err := s.saveLocked(docs); err != nil {
		return nil, err
	}
	if s.backend != nil {
		if err := s.backend.Upsert(context.Background(), []Document{doc}); err != nil {
			return nil, fmt.Errorf("同步生产向量索引失败（文档已保存在本地，可重建索引恢复）: %w", err)
		}
	}
	return &doc, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	docs, err := s.loadLocked()
	if err != nil {
		return err
	}
	next := docs[:0]
	for _, doc := range docs {
		if doc.ID != id {
			next = append(next, doc)
		}
	}
	if len(next) == len(docs) {
		return fmt.Errorf("知识库文档不存在")
	}
	if err := s.saveLocked(next); err != nil {
		return err
	}
	if s.backend != nil {
		if err := s.backend.Delete(context.Background(), []string{id}); err != nil {
			return fmt.Errorf("同步生产向量索引删除失败（可重建索引恢复）: %w", err)
		}
	}
	return nil
}

func (s *Store) List() ([]Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	docs, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].UpdatedAt.After(docs[j].UpdatedAt) })
	for i := range docs {
		docs[i].Content = ""
		docs[i].Embedding = nil
	}
	return docs, nil
}

func (s *Store) Search(query string, limit int) ([]SearchResult, error) {
	return s.SearchWithOptions(context.Background(), query, limit, SearchOptions{})
}

func (s *Store) SearchWithOptions(ctx context.Context, query string, limit int, options SearchOptions) ([]SearchResult, error) {
	queryTerms := terms(query)
	if len(queryTerms) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Search can use the persisted lexical index even when documents cannot be
	// re-embedded right now. Indexing paths continue to use loadLocked and
	// therefore still surface embedding failures to the caller.
	docs, err := s.loadRawLocked()
	if err != nil {
		return nil, err
	}
	var queryVector []float32
	if s.embedder != nil {
		// Embedding enhances the local lexical index but is not required for
		// search. Continue with keyword scoring when a provider or model is
		// temporarily unavailable, and skip vector search below.
		queryVector, err = s.embedder.Embed(ctx, query)
		if err != nil {
			queryVector = nil
		}
	}
	remoteScores := map[string]float64{}
	if s.backend != nil && len(queryVector) > 0 {
		hits, searchErr := s.backend.Search(ctx, queryVector, maxInt(limit*8, 32))
		if searchErr != nil {
			return nil, fmt.Errorf("生产向量检索失败: %w", searchErr)
		}
		for _, hit := range hits {
			remoteScores[hit.ID] = hit.Score
		}
	}

	visibleDocs := make([]Document, 0, len(docs))
	for _, doc := range docs {
		if hasScopeAccess(doc.Scopes, options.Scopes) {
			visibleDocs = append(visibleDocs, doc)
		}
	}
	results := make([]SearchResult, 0, len(visibleDocs))
	avgLength := 0.0
	for _, doc := range visibleDocs {
		avgLength += float64(len(terms(doc.Title + " " + doc.Content)))
	}
	if len(visibleDocs) > 0 {
		avgLength /= float64(len(visibleDocs))
	}
	documentFrequency := make(map[string]int)
	for _, doc := range visibleDocs {
		seen := make(map[string]bool)
		for _, term := range terms(doc.Title + " " + doc.Content) {
			if !seen[term] {
				documentFrequency[term]++
				seen[term] = true
			}
		}
	}
	for _, doc := range visibleDocs {
		score, matched := scoreDocument(doc, queryTerms, documentFrequency, len(visibleDocs), avgLength, query)
		vectorScore := cosine(queryVector, doc.Embedding)
		if s.backend != nil {
			vectorScore = remoteScores[doc.ID]
		}
		score += vectorScore * 0.65
		if score <= 0 {
			continue
		}
		results = append(results, SearchResult{
			ID:      doc.ID,
			Title:   doc.Title,
			Snippet: snippet(doc.Content, queryTerms),
			Score:   score, VectorScore: vectorScore,
			MatchedTerms: matched,
			Source:       doc.Source, Version: doc.Version, UpdatedAt: doc.UpdatedAt,
		})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if options.Reranker != nil {
		results, err = options.Reranker.Rerank(ctx, query, results)
		if err != nil {
			return nil, fmt.Errorf("RAG reranker 失败: %w", err)
		}
	}
	for i := range results {
		results[i].Rank = i + 1
	}
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func hasScopeAccess(documentScopes, requestedScopes []string) bool {
	if len(documentScopes) == 0 || len(requestedScopes) == 0 {
		return len(documentScopes) == 0
	}
	wanted := make(map[string]struct{}, len(requestedScopes))
	for _, scope := range requestedScopes {
		wanted[scope] = struct{}{}
	}
	for _, scope := range documentScopes {
		if _, ok := wanted[scope]; ok {
			return true
		}
	}
	return false
}

func (s *Store) loadLocked() ([]Document, error) {
	docs, err := s.loadRawLocked()
	if err != nil {
		return nil, err
	}
	if err := s.ensureEmbeddingsLocked(context.Background(), docs); err != nil {
		return nil, err
	}
	return docs, nil
}

func (s *Store) loadRawLocked() ([]Document, error) {
	if s.db != nil {
		rows, err := s.db.Query(`SELECT id, title, content, source, tags, scopes, version, embedding_version, embedding, updated_at FROM rag_documents`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		docs := make([]Document, 0)
		for rows.Next() {
			var doc Document
			var tags, scopes, embedding string
			var updatedAt int64
			if err := rows.Scan(&doc.ID, &doc.Title, &doc.Content, &doc.Source, &tags, &scopes, &doc.Version, &doc.EmbeddingVersion, &embedding, &updatedAt); err != nil {
				return nil, err
			}
			if err := json.Unmarshal([]byte(tags), &doc.Tags); err != nil {
				return nil, fmt.Errorf("读取 RAG 标签失败: %w", err)
			}
			if err := json.Unmarshal([]byte(scopes), &doc.Scopes); err != nil {
				return nil, fmt.Errorf("读取 RAG scopes 失败: %w", err)
			}
			if err := json.Unmarshal([]byte(embedding), &doc.Embedding); err != nil {
				return nil, fmt.Errorf("读取 RAG 向量失败: %w", err)
			}
			if updatedAt > 0 {
				doc.UpdatedAt = time.Unix(0, updatedAt)
			}
			docs = append(docs, doc)
		}
		return docs, rows.Err()
	}
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var docs []Document
	if err := json.Unmarshal(data, &docs); err != nil {
		return nil, fmt.Errorf("读取知识库失败: %w", err)
	}
	return docs, nil
}

func (s *Store) ensureEmbeddingLocked(ctx context.Context, doc *Document) error {
	if doc == nil || s.embedder == nil {
		return nil
	}
	if len(doc.Embedding) > 0 && doc.EmbeddingVersion == s.embedder.Version() {
		return nil
	}
	embedding, err := s.embedder.Embed(ctx, doc.Title+"\n"+doc.Content)
	if err != nil {
		return fmt.Errorf("生成知识库向量失败: %w", err)
	}
	doc.Embedding = embedding
	doc.EmbeddingVersion = s.embedder.Version()
	return nil
}

func (s *Store) ensureEmbeddingsLocked(ctx context.Context, docs []Document) error {
	if s.embedder == nil {
		return nil
	}
	indexes := make([]int, 0)
	values := make([]string, 0)
	for i := range docs {
		if len(docs[i].Embedding) > 0 && docs[i].EmbeddingVersion == s.embedder.Version() {
			continue
		}
		indexes = append(indexes, i)
		values = append(values, docs[i].Title+"\n"+docs[i].Content)
	}
	if len(indexes) == 0 {
		return nil
	}
	if batch, ok := s.embedder.(BatchEmbedder); ok {
		vectors, err := batch.EmbedBatch(ctx, values)
		if err != nil {
			return fmt.Errorf("批量生成知识库向量失败: %w", err)
		}
		if len(vectors) != len(indexes) {
			return fmt.Errorf("批量 Embedding 返回数量不匹配: got %d want %d", len(vectors), len(indexes))
		}
		for i, index := range indexes {
			if len(vectors[i]) == 0 {
				return fmt.Errorf("批量 Embedding 返回空向量: %d", i)
			}
			docs[index].Embedding = vectors[i]
			docs[index].EmbeddingVersion = s.embedder.Version()
		}
		return nil
	}
	for i := range docs {
		if err := s.ensureEmbeddingLocked(ctx, &docs[i]); err != nil {
			return err
		}
	}
	return nil
}

// Reindex incrementally updates documents missing the current embedding
// version and persists the upgraded index.
func (s *Store) Reindex(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	docs, err := s.loadRawLocked()
	if err != nil {
		return 0, err
	}
	versions := make([]string, len(docs))
	for i := range docs {
		versions[i] = docs[i].EmbeddingVersion
	}
	if err := s.ensureEmbeddingsLocked(ctx, docs); err != nil {
		changed := 0
		for i := range docs {
			if docs[i].EmbeddingVersion != versions[i] {
				changed++
			}
		}
		return changed, err
	}
	changed := 0
	for i := range docs {
		if docs[i].EmbeddingVersion != versions[i] {
			changed++
		}
	}
	if changed > 0 {
		err = s.saveLocked(docs)
	}
	if err == nil && s.backend != nil && len(docs) > 0 {
		err = s.backend.Upsert(ctx, docs)
	}
	return changed, err
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func normalize(vector []float32) []float32 {
	var length float64
	for _, value := range vector {
		length += float64(value * value)
	}
	if length == 0 {
		return vector
	}
	scale := float32(1 / math.Sqrt(length))
	for i := range vector {
		vector[i] *= scale
	}
	return vector
}

func cosine(left, right []float32) float64 {
	if len(left) == 0 || len(left) != len(right) {
		return 0
	}
	var score float64
	for i := range left {
		score += float64(left[i] * right[i])
	}
	return score
}

func (s *Store) saveLocked(docs []Document) error {
	if s.db != nil {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		rollback := func(err error) error { _ = tx.Rollback(); return err }
		if _, err := tx.Exec(`DELETE FROM rag_documents`); err != nil {
			return rollback(err)
		}
		statement, err := tx.Prepare(`INSERT INTO rag_documents (id, title, content, source, tags, scopes, version, embedding_version, embedding, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return rollback(err)
		}
		defer statement.Close()
		for _, doc := range docs {
			tags, err := json.Marshal(doc.Tags)
			if err != nil {
				return rollback(err)
			}
			scopes, err := json.Marshal(doc.Scopes)
			if err != nil {
				return rollback(err)
			}
			embedding, err := json.Marshal(doc.Embedding)
			if err != nil {
				return rollback(err)
			}
			if _, err := statement.Exec(doc.ID, doc.Title, doc.Content, doc.Source, string(tags), string(scopes), doc.Version, doc.EmbeddingVersion, string(embedding), doc.UpdatedAt.UnixNano()); err != nil {
				return rollback(err)
			}
		}
		return tx.Commit()
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(docs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0600)
}

func scoreDocument(doc Document, queryTerms []string, documentFrequency map[string]int, totalDocuments int, avgLength float64, query string) (float64, []string) {
	title := strings.ToLower(doc.Title)
	allTerms := terms(doc.Title + " " + doc.Content)
	termFrequency := make(map[string]int)
	for _, term := range allTerms {
		termFrequency[term]++
	}
	docLength := float64(len(allTerms))
	if avgLength <= 0 {
		avgLength = 1
	}
	var score float64
	matched := make([]string, 0, len(queryTerms))
	for _, term := range queryTerms {
		tf := termFrequency[term]
		if tf == 0 {
			continue
		}
		matched = append(matched, term)
		df := float64(documentFrequency[term])
		idf := 1.0
		if totalDocuments > 0 {
			idf = math.Log(1 + (float64(totalDocuments)-df+0.5)/(df+0.5))
		}
		bm25 := idf * (float64(tf) * 2.2) / (float64(tf) + 1.2*(1-0.75+0.75*docLength/avgLength))
		score += bm25
		if strings.Contains(title, term) {
			score += 2.5
		}
	}
	if phrase := strings.TrimSpace(strings.ToLower(query)); phrase != "" && strings.Contains(strings.ToLower(doc.Title+" "+doc.Content), phrase) {
		score += 2
	}
	return score, matched
}

func contentVersion(title, content string) string {
	digest := sha256.Sum256([]byte(title + "\x00" + content))
	return hex.EncodeToString(digest[:])
}

func terms(value string) []string {
	raw := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	seen := make(map[string]struct{})
	out := make([]string, 0, len(raw))
	for _, term := range raw {
		if len([]rune(term)) < 2 {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		out = append(out, term)
	}
	return out
}

func snippet(content string, queryTerms []string) string {
	lower := strings.ToLower(content)
	index := 0
	for _, term := range queryTerms {
		if found := strings.Index(lower, term); found >= 0 {
			index = found
			break
		}
	}
	start := index - 160
	if start < 0 {
		start = 0
	}
	end := start + 360
	if end > len(content) {
		end = len(content)
	}
	return strings.TrimSpace(content[start:end])
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

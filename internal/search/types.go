package search

// DocumentSearchRequest describes a document search query.
type DocumentSearchRequest struct {
	Query          string
	QueryEmbedding []float64
	Channels       []string
	Limit          int
	MinScore       float64
	Model          string
	UseEmbeddings  bool
	UseLexical     bool
	TrackRetrieval bool
}

// DocumentSearchResult represents a document search match.
type DocumentSearchResult struct {
	DocKey         string
	EventID        string
	Channel        string
	Title          string
	Description    string
	Snippet        string
	Score          float64
	ScoreBreakdown map[string]float64
}

// DocumentSearchResponse groups results with diagnostics.
type DocumentSearchResponse struct {
	Query         string
	Model         string
	EmbeddingUsed bool
	LexicalUsed   bool
	Results       []DocumentSearchResult
}

// SegmentSearchRequest describes a segment search query.
type SegmentSearchRequest struct {
	Query          string
	QueryEmbedding []float64
	Channel        string
	DefinitionName string
	Limit          int
	MinScore       float64
	Model          string
	UseEmbeddings  bool
}

// SegmentSearchResult represents a segment search match.
type SegmentSearchResult struct {
	SegmentID      string
	DefinitionName string
	Channel        string
	ThreadID       string
	ThreadName     string
	StartTime      int64
	EndTime        int64
	EventCount     int
	Score          float64
}

// SegmentSearchResponse groups segment results with diagnostics.
type SegmentSearchResponse struct {
	Query         string
	Model         string
	EmbeddingUsed bool
	Results       []SegmentSearchResult
}

// Embedder generates vector embeddings for text.
type Embedder interface {
	Embed(query string, model string) ([]float64, error)
}

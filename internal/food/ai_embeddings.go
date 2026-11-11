package food

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

const openAIBaseURL = "https://api.openai.com/v1"

type EmbeddingGenerator interface {
	GenerateEmbedding(ctx context.Context, text string) ([]float64, error)
}

type OpenAIEmbeddingGeneratorConfig struct {
	APIKey     string
	Model      string
	Dimensions int
	Timeout    time.Duration
	Logger     *slog.Logger
}

type OpenAIEmbeddingGenerator struct {
	client     *openai.Client
	model      string
	dimensions int
	logger     *slog.Logger
}

type embeddingRequest struct {
	Model      string `json:"model"`
	Input      string `json:"input"`
	Dimensions int    `json:"dimensions,omitempty"`
}

type embeddingResponse struct {
	Data []embeddingData `json:"data"`
}

type embeddingData struct {
	Embedding []float64 `json:"embedding"`
}

func NewOpenAIEmbeddingGenerator(cfg OpenAIEmbeddingGeneratorConfig) (*OpenAIEmbeddingGenerator, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("openai api key is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("openai embedding model is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}

	httpClient := &http.Client{Timeout: cfg.Timeout}
	client := openai.NewClient(
		option.WithAPIKey(cfg.APIKey),
		option.WithBaseURL(openAIBaseURL),
		option.WithHTTPClient(httpClient),
	)

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &OpenAIEmbeddingGenerator{client: &client, model: cfg.Model, dimensions: cfg.Dimensions, logger: logger}, nil
}

func (g *OpenAIEmbeddingGenerator) GenerateEmbedding(ctx context.Context, text string) ([]float64, error) {
	request := embeddingRequest{Model: g.model, Input: strings.TrimSpace(text), Dimensions: g.dimensions}
	start := time.Now()
	var response embeddingResponse
	err := g.client.Post(ctx, "embeddings", request, &response)
	duration := time.Since(start)
	if err != nil {
		g.logger.Error("openai embedding request failed", "model", g.model, "duration", duration, "textLength", len(text), "error", err)
		return nil, fmt.Errorf("openai embedding request failed: %w", err)
	}
	if duration > 10*time.Second {
		g.logger.Warn("openai embedding request was slow", "model", g.model, "duration", duration)
	}
	if len(response.Data) == 0 || len(response.Data[0].Embedding) == 0 {
		g.logger.Error("openai returned empty embedding", "model", g.model, "duration", duration, "textLength", len(text))
		return nil, errors.New("openai returned empty embedding")
	}
	return response.Data[0].Embedding, nil
}

func encodeFloat32Blob(values []float64) []byte {
	if len(values) == 0 {
		return nil
	}
	blob := make([]byte, len(values)*4)
	for idx, value := range values {
		binary.LittleEndian.PutUint32(blob[idx*4:(idx+1)*4], math.Float32bits(float32(value)))
	}
	return blob
}

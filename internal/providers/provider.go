package providers

import (
	"context"
	"io"

	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

type Provider interface {
	Name() string
	ListModels(ctx context.Context) ([]apitypes.ModelInfo, error)
	Stream(ctx context.Context, model string, msgs []apitypes.Message, w io.Writer) (apitypes.StreamChunk, error)
	Complete(ctx context.Context, model string, msgs []apitypes.Message) (string, error)
}

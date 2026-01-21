package api

import (
	"context"

	"github.com/binance-withdrawal/internal/forwarder"
	"github.com/binance-withdrawal/internal/model"
)

// ForwarderAdapter wraps a forwarder.Forwarder to implement TransferForwarder.
type ForwarderAdapter struct {
	fwd *forwarder.Forwarder
}

// NewForwarderAdapter creates a new adapter for the forwarder.
func NewForwarderAdapter(fwd *forwarder.Forwarder) *ForwarderAdapter {
	return &ForwarderAdapter{fwd: fwd}
}

// Forward implements TransferForwarder by delegating to the underlying forwarder.
func (a *ForwarderAdapter) Forward(ctx context.Context, transfer *model.TransferRecord) *ForwardResult {
	result := a.fwd.Forward(ctx, transfer)
	return &ForwardResult{
		TranID:     result.TranID,
		Status:     string(result.Status),
		WithdrawID: result.WithdrawID,
		Error:      result.Error,
	}
}

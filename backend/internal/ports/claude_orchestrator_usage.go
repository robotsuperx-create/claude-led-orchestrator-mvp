package ports

import (
	"context"
	"errors"
)

// ErrRunBudgetExceeded is returned by a model call once its run has spent its
// token or cost budget. The run then fails instead of spending more.
var ErrRunBudgetExceeded = errors.New("the run has used up its model budget")

// RunUsage is how much model work one run has used, and its limits. Token
// counts are what providers report. Cost is an estimate from operator-set
// prices and is zero when no price is configured. Agent sessions (agents
// mode) bill through their own harness and are not counted here.
type RunUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	Calls        int64 `json:"calls"`
	// CostMicroUSD is the estimated cost in millionths of a US dollar.
	CostMicroUSD int64 `json:"cost_micro_usd"`
	// Priced is true when every call so far had a configured price.
	Priced bool `json:"priced"`
	// BudgetTokens and BudgetMicroUSD are the limits; zero means no limit.
	BudgetTokens   int64 `json:"budget_tokens"`
	BudgetMicroUSD int64 `json:"budget_micro_usd"`
	// Exhausted is true once a call was refused because a limit was reached.
	Exhausted bool `json:"exhausted"`
}

// TotalTokens is input plus output tokens.
func (u RunUsage) TotalTokens() int64 { return u.InputTokens + u.OutputTokens }

// UsageMeter accounts for one run's model calls. Reserve is called before a
// call and refuses it once the budget is spent; Record adds what the provider
// reported after the call.
type UsageMeter interface {
	Reserve(provider ModelProvider) error
	Record(provider ModelProvider, inputTokens, outputTokens int)
}

// OrchestrationUsageReporter exposes a run's live usage.
type OrchestrationUsageReporter interface {
	UsageFor(runID string) (RunUsage, bool)
}

type usageMeterKey struct{}

// WithUsageMeter returns a context whose model calls are counted by meter.
func WithUsageMeter(ctx context.Context, meter UsageMeter) context.Context {
	if meter == nil {
		return ctx
	}
	return context.WithValue(ctx, usageMeterKey{}, meter)
}

// UsageMeterFrom returns the meter carried by ctx, or nil.
func UsageMeterFrom(ctx context.Context) UsageMeter {
	if ctx == nil {
		return nil
	}
	meter, _ := ctx.Value(usageMeterKey{}).(UsageMeter)
	return meter
}

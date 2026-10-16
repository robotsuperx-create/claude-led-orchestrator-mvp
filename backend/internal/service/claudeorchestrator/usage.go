package claudeorchestrator

import (
	"math"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ModelPrice is a provider's price in US dollars per million tokens.
type ModelPrice struct {
	InputUSDPerMTok  float64
	OutputUSDPerMTok float64
}

// UsageBudget limits how much model work one run may use. Zero limits mean
// no limit. A budget is checked before each call, so a run can exceed it by
// at most the one call that crossed it.
type UsageBudget struct {
	MaxTokens  int64
	MaxCostUSD float64
	// Prices estimate cost per provider; a provider without a price counts
	// tokens only, and the cost limit then cannot be enforced for it.
	Prices map[ports.ModelProvider]ModelPrice
}

// usageMeter is one run's ports.UsageMeter.
type usageMeter struct {
	mu     sync.Mutex
	budget UsageBudget
	usage  ports.RunUsage
}

func newUsageMeter(budget UsageBudget) *usageMeter {
	meter := &usageMeter{budget: budget}
	meter.usage.BudgetTokens = max(budget.MaxTokens, 0)
	if budget.MaxCostUSD > 0 {
		meter.usage.BudgetMicroUSD = int64(math.Round(budget.MaxCostUSD * 1e6))
	}
	meter.usage.Priced = true
	return meter
}

// Reserve refuses a call once the run's token or cost budget is spent.
func (m *usageMeter) Reserve(ports.ModelProvider) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	spentTokens := m.usage.BudgetTokens > 0 && m.usage.TotalTokens() >= m.usage.BudgetTokens
	spentCost := m.usage.BudgetMicroUSD > 0 && m.usage.CostMicroUSD >= m.usage.BudgetMicroUSD
	if spentTokens || spentCost {
		m.usage.Exhausted = true
		return ports.ErrRunBudgetExceeded
	}
	return nil
}

// Record adds one call's reported token counts and estimated cost.
func (m *usageMeter) Record(provider ports.ModelProvider, inputTokens, outputTokens int) {
	if m == nil {
		return
	}
	inputTokens, outputTokens = max(inputTokens, 0), max(outputTokens, 0)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usage.Calls++
	m.usage.InputTokens += int64(inputTokens)
	m.usage.OutputTokens += int64(outputTokens)
	price, priced := m.budget.Prices[provider]
	if !priced || (price.InputUSDPerMTok <= 0 && price.OutputUSDPerMTok <= 0) {
		m.usage.Priced = false
		return
	}
	// One US dollar per million tokens is one micro-dollar per token.
	m.usage.CostMicroUSD += int64(math.Round(float64(inputTokens)*price.InputUSDPerMTok + float64(outputTokens)*price.OutputUSDPerMTok))
}

func (m *usageMeter) snapshot() ports.RunUsage {
	if m == nil {
		return ports.RunUsage{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	usage := m.usage
	if usage.Calls == 0 {
		usage.Priced = false
	}
	return usage
}

var _ ports.UsageMeter = (*usageMeter)(nil)

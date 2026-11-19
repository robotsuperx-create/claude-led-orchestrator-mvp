package claudeorchestrator

import (
	"context"
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
// no limit. Each call holds its worst case (its input and its whole output
// limit) against the budget while it runs, so calls in flight together can
// never carry a run past its budget; only a run's first call may, when that
// one call's worst case is larger than the whole budget.
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
	// heldTokens and heldMicroUSD are what the calls in flight may still use.
	heldTokens   int64
	heldMicroUSD int64
	holds        int
	// released is closed, and replaced, whenever a hold is released.
	released chan struct{}
}

func newUsageMeter(budget UsageBudget) *usageMeter {
	meter := &usageMeter{budget: budget, released: make(chan struct{})}
	meter.usage.BudgetTokens = max(budget.MaxTokens, 0)
	if budget.MaxCostUSD > 0 {
		meter.usage.BudgetMicroUSD = int64(math.Round(budget.MaxCostUSD * 1e6))
	}
	meter.usage.Priced = true
	return meter
}

// Reserve holds a call's worst case against the budget. It refuses the call
// when what the run has spent plus that worst case is over the budget, and
// waits while the calls in flight hold what the call would need.
func (m *usageMeter) Reserve(ctx context.Context, provider ports.ModelProvider, estimate ports.CallEstimate) (func(int, int), error) {
	if m == nil {
		return func(int, int) {}, nil
	}
	input, output := max(estimate.InputTokens, 0), max(estimate.MaxOutputTokens, 0)
	tokens := int64(input) + int64(output)
	cost, _ := m.costOf(provider, input, output)
	for {
		m.mu.Lock()
		over := func(spent, held, want, budget int64) bool {
			return budget > 0 && spent+held+want > budget
		}
		spentTokens, spentCost := m.usage.TotalTokens(), m.usage.CostMicroUSD
		exhausted := (m.usage.BudgetTokens > 0 && spentTokens >= m.usage.BudgetTokens) ||
			(m.usage.BudgetMicroUSD > 0 && spentCost >= m.usage.BudgetMicroUSD)
		// The first call of a run is let through even when its worst case is
		// larger than the whole budget, so a small budget can still plan.
		first := m.usage.Calls == 0 && m.holds == 0
		if exhausted || (!first && (over(spentTokens, 0, tokens, m.usage.BudgetTokens) || over(spentCost, 0, cost, m.usage.BudgetMicroUSD))) {
			m.usage.Exhausted = true
			m.mu.Unlock()
			return nil, ports.ErrRunBudgetExceeded
		}
		if m.holds == 0 || (!over(spentTokens, m.heldTokens, tokens, m.usage.BudgetTokens) && !over(spentCost, m.heldMicroUSD, cost, m.usage.BudgetMicroUSD)) {
			m.heldTokens += tokens
			m.heldMicroUSD += cost
			m.holds++
			m.mu.Unlock()
			var once sync.Once
			return func(inputTokens, outputTokens int) {
				once.Do(func() { m.finish(provider, tokens, cost, inputTokens, outputTokens) })
			}, nil
		}
		// What is left is held by calls in flight: wait for one to finish.
		released := m.released
		m.mu.Unlock()
		select {
		case <-released:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// finish releases one call's hold and records what it used.
func (m *usageMeter) finish(provider ports.ModelProvider, heldTokens, heldMicroUSD int64, inputTokens, outputTokens int) {
	m.mu.Lock()
	m.heldTokens -= heldTokens
	m.heldMicroUSD -= heldMicroUSD
	m.holds--
	close(m.released)
	m.released = make(chan struct{})
	m.mu.Unlock()
	if inputTokens > 0 || outputTokens > 0 {
		m.Record(provider, inputTokens, outputTokens)
	}
}

// costOf is the estimated cost of a call in micro-dollars, and whether the
// provider has a price.
func (m *usageMeter) costOf(provider ports.ModelProvider, inputTokens, outputTokens int) (int64, bool) {
	price, priced := m.budget.Prices[provider]
	if !priced || (price.InputUSDPerMTok <= 0 && price.OutputUSDPerMTok <= 0) {
		return 0, false
	}
	// One US dollar per million tokens is one micro-dollar per token.
	return int64(math.Round(float64(inputTokens)*price.InputUSDPerMTok + float64(outputTokens)*price.OutputUSDPerMTok)), true
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
	cost, priced := m.costOf(provider, inputTokens, outputTokens)
	if !priced {
		m.usage.Priced = false
		return
	}
	m.usage.CostMicroUSD += cost
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

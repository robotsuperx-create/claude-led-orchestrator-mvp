package claudeorchestrator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// One dollar per million tokens both ways: a token is a micro-dollar.
func dollarMeter(maxUSD float64) *usageMeter {
	return newUsageMeter(UsageBudget{MaxCostUSD: maxUSD, Prices: map[ports.ModelProvider]ModelPrice{
		ports.ModelProviderClaude: {InputUSDPerMTok: 1, OutputUSDPerMTok: 1},
	}})
}

func reserveAsync(ctx context.Context, meter *usageMeter, estimate ports.CallEstimate) chan reserved {
	done := make(chan reserved, 1)
	go func() {
		finish, err := meter.Reserve(ctx, ports.ModelProviderClaude, estimate)
		done <- reserved{finish, err}
	}()
	return done
}

type reserved struct {
	finish func(int, int)
	err    error
}

func stillWaiting(t *testing.T, done chan reserved) {
	t.Helper()
	select {
	case got := <-done:
		t.Fatalf("Reserve returned (err %v) while the budget was held by a call in flight", got.err)
	case <-time.After(30 * time.Millisecond):
	}
}

func received(t *testing.T, done chan reserved) reserved {
	t.Helper()
	select {
	case got := <-done:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("Reserve never returned")
		return reserved{}
	}
}

// Calls in flight together never carry a run past its budget: a call whose
// worst case does not fit next to them waits, and one that could not be paid
// for even alone is refused.
func TestCallsInFlightCannotCarryARunPastItsBudget(t *testing.T) {
	meter := dollarMeter(1) // 1,000,000 micro-dollars
	call := ports.CallEstimate{InputTokens: 100_000, MaxOutputTokens: 500_000}
	ctx := context.Background()

	first, err := meter.Reserve(ctx, ports.ModelProviderClaude, call)
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	second := reserveAsync(ctx, meter, call)
	stillWaiting(t, second)

	first(80_000, 20_000) // spent 100,000: 600,000 more fits
	got := received(t, second)
	if got.err != nil {
		t.Fatalf("second reserve after the first finished: %v", got.err)
	}

	third := reserveAsync(ctx, meter, call)
	stillWaiting(t, third)
	got.finish(100_000, 400_000) // spent 600,000: another 600,000 would pass the budget
	if refused := received(t, third); !errors.Is(refused.err, ports.ErrRunBudgetExceeded) {
		t.Fatalf("third reserve = %v, want the budget refused rather than overrun", refused.err)
	}
	usage := meter.snapshot()
	if !usage.Exhausted || usage.CostMicroUSD > usage.BudgetMicroUSD || usage.Calls != 2 {
		t.Fatalf("usage = %+v, want exhausted, within budget, two calls", usage)
	}
}

func TestAFailedCallReleasesItsHoldWithoutCounting(t *testing.T) {
	meter := dollarMeter(1)
	call := ports.CallEstimate{InputTokens: 100_000, MaxOutputTokens: 500_000}
	finish, err := meter.Reserve(context.Background(), ports.ModelProviderClaude, call)
	if err != nil {
		t.Fatal(err)
	}
	waiting := reserveAsync(context.Background(), meter, call)
	stillWaiting(t, waiting)
	finish(0, 0)
	finish(0, 0) // a second finish changes nothing
	if got := received(t, waiting); got.err != nil {
		t.Fatalf("reserve after a failed call = %v", got.err)
	}
	if usage := meter.snapshot(); usage.Calls != 0 || usage.CostMicroUSD != 0 {
		t.Fatalf("usage = %+v, want the failed call not counted", usage)
	}
}

func TestWaitingForTheBudgetEndsWithTheRun(t *testing.T) {
	meter := dollarMeter(1)
	call := ports.CallEstimate{InputTokens: 100_000, MaxOutputTokens: 500_000}
	if _, err := meter.Reserve(context.Background(), ports.ModelProviderClaude, call); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	waiting := reserveAsync(ctx, meter, call)
	stillWaiting(t, waiting)
	cancel()
	if got := received(t, waiting); !errors.Is(got.err, context.Canceled) {
		t.Fatalf("reserve after cancel = %v, want context.Canceled", got.err)
	}
}

// A budget smaller than one call's worst case still lets the run's first
// call through, so a small budget can plan; nothing after it.
func TestTheFirstCallRunsEvenWhenItsWorstCaseIsOverTheBudget(t *testing.T) {
	meter := dollarMeter(0.1) // 100,000 micro-dollars
	call := ports.CallEstimate{InputTokens: 10_000, MaxOutputTokens: 200_000}
	finish, err := meter.Reserve(context.Background(), ports.ModelProviderClaude, call)
	if err != nil {
		t.Fatalf("first reserve = %v, want it let through", err)
	}
	finish(10_000, 5_000)
	if _, err := meter.Reserve(context.Background(), ports.ModelProviderClaude, call); !errors.Is(err, ports.ErrRunBudgetExceeded) {
		t.Fatalf("second reserve = %v, want refused", err)
	}
}

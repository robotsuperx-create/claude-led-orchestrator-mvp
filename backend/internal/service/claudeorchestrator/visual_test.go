package claudeorchestrator

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type cameraFake struct {
	mu     sync.Mutex
	rounds []int
	shots  []ports.VisualShot
	err    error
}

func (c *cameraFake) Capture(_ context.Context, _ string, round int, worktree string) ([]ports.VisualShot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if worktree == "" {
		return nil, errors.New("no worktree")
	}
	c.rounds = append(c.rounds, round)
	return c.shots, c.err
}

type criticFake struct {
	mu       sync.Mutex
	requests []ports.VisualCritiqueRequest
	verdicts []ports.VisualCritique
	err      error
}

func (c *criticFake) Critique(_ context.Context, request ports.VisualCritiqueRequest) (ports.VisualCritique, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, request)
	if c.err != nil {
		return ports.VisualCritique{}, c.err
	}
	verdict := c.verdicts[0]
	if len(c.verdicts) > 1 {
		c.verdicts = c.verdicts[1:]
	}
	return verdict, nil
}

func visualService(t *testing.T, review *VisualReview, execute func(context.Context, ports.WorkerRequest) (ports.WorkerExecution, error)) (*Service, *[]ports.ReviewRequest) {
	t.Helper()
	plan := ports.ExecutionPlan{Subtasks: []ports.PlannedSubtask{{ID: "page", Title: "Landing page", Instructions: "Red #F40009, Reem Kufi", WorkerID: "w"}}}
	var reviews []ports.ReviewRequest
	model := &modelFake{
		planFn: func(context.Context, ports.PlanRequest) (ports.ExecutionPlan, error) { return plan, nil },
		reviewFn: func(_ context.Context, request ports.ReviewRequest) (ports.ReviewDecision, error) {
			reviews = append(reviews, request)
			return ports.ReviewDecision{Decision: ports.ReviewOutcomeApprove}, nil
		},
	}
	validator := &validatorFake{validateFn: func(context.Context, ports.ValidationRequest) (ports.ValidationReport, error) {
		return ports.ValidationReport{Passed: true}, nil
	}}
	return New(Dependencies{Model: model, Worker: workerRuntimeFunc(execute), Validator: validator, Memory: &memoryFake{}, Visual: review, WriterProvider: ports.ModelProviderClaude}), &reviews
}

func phoneShot(name string) ports.VisualShot {
	return ports.VisualShot{Page: "index.html", Viewport: "phone", Width: 390, JPEG: []byte{0xFF, 0xD8}, Findings: []string{"The page scrolls sideways."}, File: "/data/visual/run/" + name}
}

// The critic looks, a writer polishes what it asked for, and the critic looks
// again until the pages reach the target.
func TestVisualReviewPolishesUntilThePagesScoreHighEnough(t *testing.T) {
	camera := &cameraFake{shots: []ports.VisualShot{phoneShot("r1-index-phone.jpg")}}
	critic := &criticFake{verdicts: []ports.VisualCritique{
		{Score: 6, Summary: "Broken on phones.", Fixes: []string{"Stack the hero on phones", "Raise body contrast to #333"}},
		{Score: 9.2, Summary: "Both fixes landed."},
	}}
	var mu sync.Mutex
	var polished []ports.WorkerRequest
	service, reviews := visualService(t, &VisualReview{Capturer: camera, Critic: critic, Rounds: 2}, func(_ context.Context, request ports.WorkerRequest) (ports.WorkerExecution, error) {
		if strings.HasPrefix(request.Task.ID, "visual-") {
			mu.Lock()
			polished = append(polished, request)
			mu.Unlock()
		}
		return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
	})
	result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "r", Task: "a landing page", WorktreePath: "/work/tree"})
	if err != nil || result.State != ports.RunStateCompleted {
		t.Fatalf("Run = %+v, %v", result.State, err)
	}
	if len(camera.rounds) != 2 || len(critic.requests) != 2 {
		t.Fatalf("looked %v times, judged %d times; want 2 each", camera.rounds, len(critic.requests))
	}
	first, second := critic.requests[0], critic.requests[1]
	if first.Provider != ports.ModelProviderClaude || !strings.Contains(first.Brief, "Reem Kufi") || first.Task != "a landing page" || len(first.PreviousFixes) != 0 {
		t.Fatalf("first critique request = %+v", first)
	}
	if strings.Join(second.PreviousFixes, "|") != "Stack the hero on phones|Raise body contrast to #333" || second.Round != 2 {
		t.Fatalf("the second look was not told what to check: %+v", second)
	}
	if len(polished) != 1 {
		t.Fatalf("%d polish subtasks ran, want 1", len(polished))
	}
	task := polished[0].Task
	if task.ID != "visual-1" || task.Provider != ports.ModelProviderClaude || task.Metadata[ports.SubtaskMetadataKeyWorktreePath] != "/work/tree" ||
		!strings.Contains(task.Instructions, "1. Stack the hero on phones") || !strings.Contains(task.Instructions, "index.html at phone: The page scrolls sideways.") ||
		!strings.Contains(strings.Join(task.Skills, ","), "design-taste") {
		t.Fatalf("polish task = %+v", task)
	}
	if task.Tier != ports.SubtaskTierDesign || len(task.Looks) == 0 || task.Looks[0].Viewport == "" {
		t.Fatalf("the polish writer is not shown what the art director saw: tier %q, %d looks", task.Tier, len(task.Looks))
	}
	if len(result.Visual) != 2 || result.Visual[0].Polish != "visual-1" || result.Visual[1].Score != 9.2 || result.Visual[0].Shots[0].Name != "r1-index-phone.jpg" {
		t.Fatalf("visual rounds = %+v", result.Visual)
	}
	if len(*reviews) != 1 || len((*reviews)[0].Visual) != 2 {
		t.Fatal("the final review did not see the visual rounds")
	}
	if len(result.Plan.Subtasks) != 2 || result.Plan.Subtasks[1].ID != "visual-1" {
		t.Fatalf("plan = %+v, want the polish subtask added", result.Plan.Subtasks)
	}
	if path, ok := service.VisualShotFile("r", "r1-index-phone.jpg"); !ok || path != "/data/visual/run/r1-index-phone.jpg" {
		t.Fatalf("VisualShotFile = %q, %v", path, ok)
	}
	if _, ok := service.VisualShotFile("r", "../../etc/passwd"); ok {
		t.Fatal("a picture the review did not keep was found")
	}
	if rounds := service.VisualFor("r"); len(rounds) != 2 {
		t.Fatalf("VisualFor = %d rounds", len(rounds))
	}
	if on, _ := service.VisualReviewStatus(); !on {
		t.Fatal("the visual review reports itself off")
	}
}

// The loop stops when the rounds are spent, even below the target, and a
// failure to photograph or judge never fails the run.
func TestVisualReviewStopsAtTheRoundLimitAndNeverFailsTheRun(t *testing.T) {
	camera := &cameraFake{shots: []ports.VisualShot{phoneShot("r1-index-phone.jpg")}}
	critic := &criticFake{verdicts: []ports.VisualCritique{{Score: 5, Fixes: []string{"Fix everything"}}}}
	polishes := 0
	service, _ := visualService(t, &VisualReview{Capturer: camera, Critic: critic, Rounds: 1}, func(_ context.Context, request ports.WorkerRequest) (ports.WorkerExecution, error) {
		if strings.HasPrefix(request.Task.ID, "visual-") {
			polishes++
		}
		return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
	})
	result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "r", Task: "t", WorktreePath: "/work/tree"})
	if err != nil || result.State != ports.RunStateCompleted || polishes != 1 || len(result.Visual) != 2 || result.Visual[1].Polish != "" {
		t.Fatalf("Run = %v, %v; %d polishes, rounds %+v", result.State, err, polishes, result.Visual)
	}

	for name, review := range map[string]*VisualReview{
		"camera fails": {Capturer: &cameraFake{err: errors.New("chrome crashed at C:\\Users\\me")}, Critic: &criticFake{}, Rounds: 2},
		"critic fails": {Capturer: &cameraFake{shots: []ports.VisualShot{phoneShot("r1-index-phone.jpg")}}, Critic: &criticFake{err: errors.New("overloaded")}, Rounds: 2},
	} {
		t.Run(name, func(t *testing.T) {
			service, reviews := visualService(t, review, func(context.Context, ports.WorkerRequest) (ports.WorkerExecution, error) {
				return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
			})
			result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "r", Task: "t", WorktreePath: "/work/tree"})
			if err != nil || result.State != ports.RunStateCompleted || len(*reviews) != 1 {
				t.Fatalf("Run = %v, %v", result.State, err)
			}
			if len(result.Visual) != 1 || result.Visual[0].Error == "" || len(result.Plan.Subtasks) != 1 {
				t.Fatalf("visual = %+v", result.Visual)
			}
		})
	}
}

// A run without web pages, or a service without a review, is untouched.
func TestVisualReviewLeavesRunsWithoutPagesAlone(t *testing.T) {
	critic := &criticFake{verdicts: []ports.VisualCritique{{Score: 1}}}
	service, _ := visualService(t, &VisualReview{Capturer: &cameraFake{}, Critic: critic, Rounds: 2}, func(context.Context, ports.WorkerRequest) (ports.WorkerExecution, error) {
		return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
	})
	result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "r", Task: "t", WorktreePath: "/work/tree"})
	if err != nil || result.State != ports.RunStateCompleted || len(result.Visual) != 0 || len(critic.requests) != 0 {
		t.Fatalf("Run = %v, %v, visual %+v", result.State, err, result.Visual)
	}
	off := New(Dependencies{VisualUnavailable: ports.VisualReasonNoBrowser})
	if on, reason := off.VisualReviewStatus(); on || reason != ports.VisualReasonNoBrowser {
		t.Fatalf("status = %v, %q", on, reason)
	}
}

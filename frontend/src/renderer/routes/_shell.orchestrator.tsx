import { createFileRoute } from "@tanstack/react-router";
import { ClaudeOrchestratorView } from "../components/ClaudeOrchestratorView";

export const Route = createFileRoute("/_shell/orchestrator")({ component: ClaudeOrchestratorView });

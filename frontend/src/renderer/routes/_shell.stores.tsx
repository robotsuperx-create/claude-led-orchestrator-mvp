import { createFileRoute } from "@tanstack/react-router";
import { StoresView } from "../components/StoresView";

export const Route = createFileRoute("/_shell/stores")({ component: StoresView });

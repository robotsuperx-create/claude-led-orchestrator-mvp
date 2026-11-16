import type { ProjectSource } from "@aoagents/product-ui";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { AlertTriangle, Cloud, Folder, Folders, FolderOpen, GitFork, MessageSquarePlus, Star } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";
import { useCloudGate } from "../hooks/useCloudGate";
import { useSystemRequirementsGate } from "../hooks/useSystemRequirementsGate";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { appI18n } from "../i18n";
import { aoBridge } from "../lib/bridge";
import { getProjectLastOpenedAt } from "../lib/project-history";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import { useShell } from "../lib/shell-context";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import {
	STANDALONE_PROJECT_KIND,
	STANDALONE_WORKSPACE_ID,
	type WorkspaceSummary,
} from "../types/workspace";
import { CreateProjectFlow } from "./CreateProjectFlow";
import { HomeDeck } from "./deck/HomeDeck";
import { DaemonStartupLoader } from "./DaemonStartupLoader";
import { GitHubOnboardingNotice } from "./GitHubOnboardingNotice";
import { Badge } from "./ui/badge";

/**
 * Home is the X-ROBOT observation deck (see deck/HomeDeck). Its projects
 * section keeps the landing contracts:
 * - "Star us" is a quiet text link with dashed underline on hover.
 * - Primary actions are a 2×2 grid that always includes the standalone-agent
 *   action. With Developer Mode and Cloud enabled, a full-width Cloud action
 *   sits beneath the grid rather than displacing standalone.
 *   Connect Mobile is settings-only — not here.
 * - With no projects the heading is Get started and Recent projects stays
 *   hidden; otherwise keep Jump back paired with Recent projects. The
 *   Scratchpad (standalone agents) is not a project: it never appears in
 *   Recent projects or counts toward the heading.
 */
const GITHUB_REPOSITORY_URL = "https://github.com/robotsuperx-create/claude-led-orchestrator-mvp";
const RECENT_PROJECT_LIMIT = 3;
const HOME_BUTTON_CLASS = "deck-action";
const HOME_ICON_SLOT_CLASS = "deck-action-icon";
const HOME_PROJECT_ICON_CLASS = "deck-action-icon";

function latestProjectTimestamp(project: WorkspaceSummary): string {
	return [
		getProjectLastOpenedAt(project.id),
		...project.sessions.flatMap((session) => [session.lastUserMessageAt, session.createdAt]),
	]
		.filter((timestamp): timestamp is string => Boolean(timestamp))
		.sort()
		.at(-1) ?? "";
}

function relativeProjectTime(timestamp: string | undefined, emptyLabel: string, justNowLabel: string): string {
	if (!timestamp) return emptyLabel;
	const elapsedMinutes = Math.floor((Date.now() - new Date(timestamp).getTime()) / 60_000);
	if (!Number.isFinite(elapsedMinutes) || elapsedMinutes < 1) return justNowLabel;
	const unit: Intl.RelativeTimeFormatUnit = elapsedMinutes < 60 ? "minute" : elapsedMinutes < 1_440 ? "hour" : "day";
	const amount = unit === "minute" ? elapsedMinutes : unit === "hour" ? Math.floor(elapsedMinutes / 60) : Math.floor(elapsedMinutes / 1_440);
	return new Intl.RelativeTimeFormat(appI18n.resolvedLanguage ?? appI18n.language, { numeric: "always" }).format(-amount, unit);
}

function sortProjectsByActivity(projects: WorkspaceSummary[]): WorkspaceSummary[] {
	return projects
		.slice()
		.sort((left, right) => latestProjectTimestamp(right).localeCompare(latestProjectTimestamp(left)));
}

function ProjectRow({ project, onClick, emptyTimeLabel, justNowLabel }: { project: WorkspaceSummary; onClick: () => void; emptyTimeLabel: string; justNowLabel: string }) {
	const { t } = useTranslation();
	const lastOpenedAt = getProjectLastOpenedAt(project.id);
	const latestProjectFact = latestProjectTimestamp(project) || lastOpenedAt;

	return (
		<button
			// Host must use NAV_ROW_HIGHLIGHT_HOST_CLASS — pill owns the fill.
			className="deck-project"
			onClick={onClick}
			type="button"
		>
			<span className={HOME_PROJECT_ICON_CLASS} aria-hidden="true">
				{project.folderMissing ? <AlertTriangle strokeWidth={1.8} className="text-warning" /> : <Folder strokeWidth={1.8} />}
			</span>
			<span className="deck-project-text">
				<span className="deck-project-name">
					<span className="truncate">{project.name}</span>
					{project.folderMissing ? (
						<Badge variant="warning" className="h-4 shrink-0 px-1.5 text-2xs">{t("home.folderMissing")}</Badge>
					) : null}
				</span>
				<span className="small t-2 truncate"><bdi>{project.path}</bdi></span>
			</span>
			<span className="deck-project-when small t-2 n">
				{relativeProjectTime(latestProjectFact, emptyTimeLabel, justNowLabel)}
			</span>
		</button>
	);
}

function HomeActionCard({
	centerContent = false,
	disabled,
	icon,
	label,
	onClick,
}: {
	centerContent?: boolean;
	disabled?: boolean;
	icon: ReactNode;
	label: string;
	onClick?: () => void;
}) {
	return (
		<button
			className={cn(HOME_BUTTON_CLASS, centerContent && "deck-action--center")}
			disabled={disabled}
			onClick={onClick}
			type="button"
		>
			<span className={HOME_ICON_SLOT_CLASS} aria-hidden="true">
				{icon}
			</span>
			<span className="deck-action-label">{label}</span>
		</button>
	);
}

export function HomePage() {
	const navigate = useNavigate();
	const { t } = useTranslation();
	const requestNewTask = useUiStore((state) => state.requestNewTask);
	const developerMode = useUiStore((state) => state.developerMode);
	const { cloneProject, createProject, daemonStatus, initializeProjectRepository, workspaceStartupState } =
		useShell();
	const { blocked: requirementsBlocked } = useSystemRequirementsGate();
	const { cloudEnabled } = useCloudGate();
	const workspaceQuery = useWorkspaceQuery();
	const [sourceSignal, setSourceSignal] = useState<{ source: ProjectSource | "cloud"; nonce: number } | null>(null);
	const projects = workspaceQuery.data ?? [];
	const recentProjects = useMemo(
		() =>
			sortProjectsByActivity(projects.filter((project) => project.kind !== STANDALONE_PROJECT_KIND)).slice(
				0,
				RECENT_PROJECT_LIMIT,
			),
		[projects],
	);

	const isDaemonReady = usesPreviewWorkspaceData || daemonStatus.state === "ready";
	const daemonHasFailed = Boolean(daemonStatus.code);
	const showStartup =
		!daemonHasFailed &&
		(!isDaemonReady ||
			workspaceStartupState === "loading" ||
			(!workspaceQuery.isSuccess && !workspaceQuery.isError) ||
			requirementsBlocked);

	if (showStartup) return <DaemonStartupLoader />;

	const requestSource = (source: ProjectSource | "cloud") => {
		setSourceSignal({ source, nonce: Date.now() });
	};

	const openProject = (projectId: string) => {
		void navigate({ to: "/projects/$projectId", params: { projectId } });
	};
	const openExistingProject = (path: string) => {
		const project = projects.find((candidate) => candidate.path === path);
		if (project) void navigate({ to: "/projects/$projectId", params: { projectId: project.id } });
	};

	if (workspaceStartupState === "error" || workspaceQuery.isError) {
		return (
			<div className="flex min-h-full items-center justify-center px-6 py-16">
				<p className="text-center text-xs text-passive">{t("shell.couldNotLoadProjects")}</p>
			</div>
		);
	}

	return (
		<>
			<HomeDeck
				projects={
					<>
						<div className="sec-h">
							<h2 className="d1">{recentProjects.length === 0 ? t("home.getStarted") : t("home.jumpBack")}</h2>
							{/* Quiet text link — not a button or accent. Dashed underline only on hover. */}
							<button className="deck-star" onClick={() => void aoBridge.app.openExternal(GITHUB_REPOSITORY_URL)} type="button">
								<Star className="size-3.5" strokeWidth={1.8} aria-hidden="true" />
								{t("home.starUs")}
							</button>
						</div>
						<div className="deck-actions">
							<HomeActionCard
								icon={<GitFork strokeWidth={1.8} />}
								label={t("createProject.cloneFromGit")}
								onClick={() => requestSource("clone")}
							/>
							<HomeActionCard
								icon={<FolderOpen strokeWidth={1.8} />}
								label={t("createProject.openLocal")}
								onClick={() => requestSource("local")}
							/>
							<HomeActionCard
								icon={<Folders strokeWidth={1.8} />}
								label={t("createProject.addWorkspace")}
								onClick={() => requestSource("workspace")}
							/>
							<HomeActionCard
								icon={<MessageSquarePlus strokeWidth={1.8} />}
								label={t("home.newStandaloneAgent")}
								onClick={() => requestNewTask(STANDALONE_WORKSPACE_ID)}
							/>
							{developerMode && cloudEnabled ? (
								<div className="deck-actions-wide">
									<HomeActionCard
										centerContent
										icon={<Cloud strokeWidth={1.8} />}
										label={t("createProject.cloudTitle")}
										onClick={() => requestSource("cloud")}
									/>
								</div>
							) : null}
						</div>

						{recentProjects.length > 0 && (
							<>
								<h3 className="d2">{t("home.recentProjects")}</h3>
								<div className="deck-projects">
									{recentProjects.map((project) => (
										<ProjectRow
											key={project.id}
											project={project}
											onClick={() => openProject(project.id)}
											emptyTimeLabel={t("home.never")}
											justNowLabel={t("time.justNow")}
										/>
									))}
								</div>
							</>
						)}

						<GitHubOnboardingNotice />
					</>
				}
			/>

			<CreateProjectFlow
				existingProjectNames={projects.map((project) => project.name)}
				existingProjectPaths={projects.map((project) => project.path)}
				mode="choose"
				onCloneProject={cloneProject}
				onCreateProject={createProject}
				onCreateStandaloneAgent={() => requestNewTask(STANDALONE_WORKSPACE_ID)}
				onInitializeProject={initializeProjectRepository}
				onOpenExistingProject={openExistingProject}
				sourceSignal={sourceSignal}
			/>
		</>
	);
}

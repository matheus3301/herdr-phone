import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ChevronDown, ChevronRight, Keyboard, MessageSquare, Pencil, SquareTerminal } from "lucide-react";
import { StatusDot } from "@/components/status-dot";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { AgentPromptSheet, AgentKeysSheet } from "@/components/agent-actions";
import { RenameDialog } from "@/components/rename-dialog";
import { EmptyState } from "@/components/states";
import { useAppState } from "@/hooks/use-app-store";
import { useMutations } from "@/hooks/use-mutations";
import { selectionStore } from "@/lib/selection";
import { groupAgents } from "@/lib/triage";
import { shortPath, statusLabel } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { Agent } from "@/lib/types";

function AgentRow({ agent }: { agent: Agent }) {
  const navigate = useNavigate();
  const { run } = useMutations();
  const blocked = agent.status === "blocked";

  function open() {
    selectionStore.set(agent.paneId);
    void run("workspace.focus", { workspace_id: agent.workspaceId });
    void run("tab.focus", { tab_id: agent.tabId });
    void run("agent.focus", { target: agent.name });
    navigate("/");
  }

  return (
    <li
      className={cn(
        "rounded-[10px] border bg-hull p-3",
        blocked ? "border-flare/50" : "border-frame",
      )}
    >
      <div className="flex items-center gap-2">
        <StatusDot status={agent.status} />
        <span className="truncate text-sm font-medium text-mist">{agent.name}</span>
        <Badge tone="neutral">{agent.kind}</Badge>
      </div>
      {blocked && agent.title && (
        <p className="mt-1.5 rounded-[8px] border border-flare/30 bg-flare/10 px-2 py-1 text-sm text-mist">
          “{agent.title}”
        </p>
      )}
      {!blocked && agent.title && <p className="mt-1 truncate text-[13px] text-muted-ink">{agent.title}</p>}
      <p className="mt-1 truncate font-utility text-[11px] text-muted-ink" title={agent.cwd}>
        {statusLabel(agent.status)} · {shortPath(agent.cwd)}
      </p>
      <div className="mt-2 flex items-center gap-2">
        <Button variant={blocked ? "primary" : "default"} size="sm" onClick={open} className="flex-1">
          <SquareTerminal className="size-4" /> Open terminal
        </Button>
        <AgentPromptSheet
          agent={agent}
          trigger={
            <Button variant="outline" size="sm" aria-label={`Prompt ${agent.name}`}>
              <MessageSquare className="size-4" /> Prompt
            </Button>
          }
        />
        <AgentKeysSheet
          agent={agent}
          trigger={
            <Button variant="ghost" size="icon" aria-label={`Send keys to ${agent.name}`}>
              <Keyboard className="size-4" />
            </Button>
          }
        />
        <RenameDialog
          title="Rename agent"
          operation="agent.rename"
          idKey="pane_id"
          idValue={agent.paneId}
          valueKey="name"
          current={agent.name}
          trigger={
            <Button variant="ghost" size="icon" aria-label={`Rename ${agent.name}`}>
              <Pencil className="size-4" />
            </Button>
          }
        />
      </div>
    </li>
  );
}

export function HerdRoute() {
  const { snapshot } = useAppState();

  const [quietOpen, setQuietOpen] = useState(false);

  const groups = useMemo(() => (snapshot ? groupAgents(snapshot.agents) : []), [snapshot]);

  if (!snapshot) return null;
  if (snapshot.agents.length === 0) {
    return (
      <div className="flex h-full items-center justify-center">
        <EmptyState
          title="No agents running"
          description="Open a shell pane, then use its actions (⋯) to start an agent — it will appear here."
        />
      </div>
    );
  }

  return (
    <div className="h-full overflow-y-auto px-3 py-3">
      {groups.map((group) => {
        if (group.agents.length === 0) return null;
        if (group.key === "quiet") {
          return (
            <section key={group.key} className="mt-4">
              <button
                type="button"
                onClick={() => setQuietOpen((o) => !o)}
                className="flex w-full items-center gap-2 py-1 font-utility text-[11px] uppercase tracking-wider text-muted-ink focus-visible:outline-2 focus-visible:outline-brass"
                aria-expanded={quietOpen}
              >
                {quietOpen ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
                {group.title} · {group.agents.length}
              </button>
              {quietOpen && (
                <ul className="mt-2 flex flex-col gap-2">
                  {group.agents.map((a) => (
                    <AgentRow key={a.paneId} agent={a} />
                  ))}
                </ul>
              )}
            </section>
          );
        }
        return (
          <section key={group.key} className="mt-4 first:mt-1">
            <h2
              className={cn(
                "py-1 font-utility text-[11px] uppercase tracking-wider",
                group.key === "blocked" ? "text-flare" : "text-tide",
              )}
            >
              {group.title} · {group.agents.length}
            </h2>
            <ul className="mt-1 flex flex-col gap-2">
              {group.agents.map((a) => (
                <AgentRow key={a.paneId} agent={a} />
              ))}
            </ul>
          </section>
        );
      })}
    </div>
  );
}

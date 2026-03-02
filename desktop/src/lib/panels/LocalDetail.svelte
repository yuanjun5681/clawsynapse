<script lang="ts">
  import type { Snippet } from "svelte";
  import type { MonitorStatus, ContainerInfo } from "../canvas/CanvasView.svelte";
  import ActivityFeed from "./ActivityFeed.svelte";

  interface Props {
    status: MonitorStatus | null;
    containers: ContainerInfo[];
    backendStatus: "running" | "starting" | "stopped";
    chatState: "idle" | "thinking" | "streaming" | "done";
    children: Snippet;
  }

  let { status, containers, backendStatus, chatState, children }: Props = $props();

  let uptimeStr = $derived.by(() => {
    if (!status) return "--";
    const s = status.uptime;
    if (s < 60) return `${Math.floor(s)}s`;
    if (s < 3600) return `${Math.floor(s / 60)}m`;
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    return `${h}h ${m}m`;
  });

  let statusColor = $derived(
    backendStatus === "running"
      ? "var(--green)"
      : backendStatus === "starting"
        ? "var(--yellow)"
        : "var(--red)"
  );
</script>

<div class="local-detail">
  <!-- Scrollable info area -->
  <div class="info-scroll">
    <!-- Status section -->
    <div class="section">
      <div class="section-title">
        <span class="status-dot" style="background:{statusColor}"></span>
        NanoClaw
      </div>
      {#if status}
        <div class="stat-grid">
          <div class="stat">
            <span class="stat-label">Uptime</span>
            <span class="stat-value">{uptimeStr}</span>
          </div>
          <div class="stat">
            <span class="stat-label">Memory</span>
            <span class="stat-value">{status.memoryMB}MB</span>
          </div>
          <div class="stat">
            <span class="stat-label">Containers</span>
            <span class="stat-value">{status.activeContainers}/{status.maxContainers}</span>
          </div>
          <div class="stat">
            <span class="stat-label">Groups</span>
            <span class="stat-value">{status.registeredGroups}</span>
          </div>
        </div>
      {:else}
        <div class="stat-placeholder">Connecting...</div>
      {/if}
    </div>

    <!-- Containers section -->
    {#if containers.length > 0}
      <div class="section">
        <div class="section-title">Containers</div>
        <div class="container-list">
          {#each containers as c}
            <div class="container-item">
              <span class="container-dot" class:running={c.status.startsWith("Up")}></span>
              <span class="container-name">{c.groupFolder || c.name}</span>
              <span class="container-status">{c.status}</span>
            </div>
          {/each}
        </div>
      </div>
    {/if}

    <!-- Activity Feed -->
    <div class="section">
      <ActivityFeed />
    </div>
  </div>

  <!-- Chat section (fixed at bottom) -->
  <div class="chat-fixed">
    <div class="divider"></div>
    <div class="section chat-section">
      <div class="section-title">Chat</div>
      <div class="chat-wrapper">
        {@render children()}
      </div>
    </div>
  </div>
</div>

<style>
  .local-detail {
    display: flex;
    flex-direction: column;
    height: 100%;
  }

  .info-scroll {
    overflow-y: auto;
    flex-shrink: 1;
  }

  .chat-fixed {
    flex: 1;
    min-height: 200px;
    display: flex;
    flex-direction: column;
  }

  .section {
    padding: 12px 16px;
  }

  .section-title {
    font-size: 12px;
    font-weight: 600;
    color: var(--text);
    margin-bottom: 8px;
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .status-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    display: inline-block;
  }

  .stat-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 6px;
  }

  .stat {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .stat-label {
    font-size: 10px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.5px;
  }

  .stat-value {
    font-size: 13px;
    color: var(--text);
    font-weight: 500;
  }

  .stat-placeholder {
    color: var(--text-muted);
    font-size: 12px;
  }

  .container-list {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .container-item {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 11px;
    padding: 4px 0;
  }

  .container-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-muted);
    flex-shrink: 0;
  }

  .container-dot.running {
    background: var(--green);
  }

  .container-name {
    color: var(--text);
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .container-status {
    color: var(--text-muted);
    font-size: 10px;
    flex-shrink: 0;
  }

  .divider {
    height: 1px;
    background: var(--border);
    margin: 0 16px;
  }

  .chat-section {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 0;
  }

  .chat-wrapper {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 0;
    overflow: hidden;
  }
</style>

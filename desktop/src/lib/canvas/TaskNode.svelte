<script lang="ts">
  import type { TaskInfo } from "./CanvasView.svelte";

  interface Props {
    x: number;
    y: number;
    info: TaskInfo;
  }

  let { x, y, info }: Props = $props();

  let isRunning = $derived(info.status === "running");

  let nextRunLabel = $derived.by(() => {
    if (!info.next_run) return "";
    const diff = new Date(info.next_run).getTime() - Date.now();
    if (diff <= 0) return "now";
    if (diff < 60_000) return `${Math.ceil(diff / 1000)}s`;
    if (diff < 3600_000) return `${Math.ceil(diff / 60_000)}m`;
    return `${Math.floor(diff / 3600_000)}h`;
  });
</script>

<g class="task-node">
  <!-- Small circle for task -->
  <circle
    cx={x}
    cy={y}
    r="10"
    fill="#0a0a0a"
    stroke={isRunning ? "var(--accent)" : "var(--text-muted)"}
    stroke-width="1"
    class:task-running={isRunning}
  />

  <!-- Clock icon -->
  <text
    {x}
    y={y + 1}
    text-anchor="middle"
    dominant-baseline="central"
    fill={isRunning ? "var(--accent)" : "var(--text-muted)"}
    font-size="9"
  >
    T
  </text>

  <!-- Next run label -->
  {#if nextRunLabel}
    <text
      {x}
      y={y + 20}
      text-anchor="middle"
      fill="var(--text-muted)"
      font-size="8"
    >
      {nextRunLabel}
    </text>
  {/if}
</g>

<style>
  .task-running circle {
    animation: task-pulse 1.5s ease-in-out infinite;
  }

  @keyframes task-pulse {
    0%, 100% {
      opacity: 1;
    }
    50% {
      opacity: 0.5;
    }
  }
</style>

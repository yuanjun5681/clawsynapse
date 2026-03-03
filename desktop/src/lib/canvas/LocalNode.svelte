<script lang="ts">
  import type { MonitorStatus } from "./CanvasView.svelte";

  interface Props {
    x: number;
    y: number;
    status: MonitorStatus | null;
    containerCount: number;
    selected?: boolean;
  }

  let { x, y, status, containerCount, selected = false }: Props = $props();

  let uptimeStr = $derived.by(() => {
    if (!status) return "--";
    const s = status.uptime;
    if (s < 60) return `${Math.floor(s)}s`;
    if (s < 3600) return `${Math.floor(s / 60)}m`;
    return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
  });
</script>

<g class="local-node">
  <!-- Selection ring -->
  {#if selected}
    <circle cx={x} cy={y} r="44" fill="none" stroke="var(--accent)" stroke-width="2" opacity="0.5" class="select-ring" />
  {/if}
  <!-- Outer glow ring -->
  <circle cx={x} cy={y} r="38" fill="none" stroke="var(--accent)" stroke-width="1.5" opacity="0.3" class="glow-ring" />

  <!-- Main circle -->
  <circle cx={x} cy={y} r="32" fill="#111" stroke="var(--accent)" stroke-width="2" class="main-circle" />

  <!-- Inner icon -->
  <text
    {x}
    y={y + 1}
    text-anchor="middle"
    dominant-baseline="central"
    fill="var(--accent)"
    font-size="20"
  >
    N
  </text>

  <!-- Label below -->
  <text
    {x}
    y={y + 50}
    text-anchor="middle"
    fill="var(--text)"
    font-size="11"
    font-weight="500"
  >
    NanoClaw
  </text>

  <!-- Stats -->
  {#if status}
    <text
      {x}
      y={y + 64}
      text-anchor="middle"
      fill="var(--text-muted)"
      font-size="9"
    >
      {uptimeStr} | {status.memoryMB}MB | {containerCount}/{status.maxContainers}
    </text>
  {/if}
</g>

<style>
  .local-node {
    cursor: pointer;
  }

  .main-circle {
    filter: drop-shadow(0 0 var(--glow-spread) rgba(var(--accent-rgb), var(--glow-opacity)));
  }

  .local-node:hover circle {
    filter: brightness(1.2);
  }

  .select-ring {
    animation: select-glow 1.5s ease-in-out infinite;
  }

  @keyframes select-glow {
    0%, 100% { opacity: 0.5; }
    50% { opacity: 0.25; }
  }

  .glow-ring {
    animation: pulse-ring 3s ease-in-out infinite;
  }

  @keyframes pulse-ring {
    0%, 100% {
      r: 38;
      opacity: 0.3;
    }
    50% {
      r: 42;
      opacity: 0.15;
    }
  }
</style>

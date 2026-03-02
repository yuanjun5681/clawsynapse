<script lang="ts">
  import type { ContainerInfo } from "./CanvasView.svelte";

  interface Props {
    x: number;
    y: number;
    info: ContainerInfo;
    selected?: boolean;
  }

  let { x, y, info, selected = false }: Props = $props();

  let isRunning = $derived(info.status.toLowerCase().startsWith("up"));
  let label = $derived(
    info.groupFolder || info.name.replace("nanoclaw-", "").split("-")[0]
  );
</script>

<g class="container-node" class:running={isRunning}>
  <!-- Selection ring -->
  {#if selected}
    <circle cx={x} cy={y} r="20" fill="none" stroke={isRunning ? "var(--green)" : "var(--text-muted)"} stroke-width="2" opacity="0.5" class="select-ring" />
  {/if}
  <!-- Container circle -->
  <circle
    cx={x}
    cy={y}
    r="14"
    fill="#0a0a0a"
    stroke={isRunning ? "var(--green)" : "var(--text-muted)"}
    stroke-width="1.5"
    class:pulse={isRunning}
  />

  <!-- Docker icon (simple box) -->
  <rect
    x={x - 5}
    y={y - 4}
    width="10"
    height="8"
    rx="1"
    fill="none"
    stroke={isRunning ? "var(--green)" : "var(--text-muted)"}
    stroke-width="1"
  />

  <!-- Label -->
  <text
    {x}
    y={y + 24}
    text-anchor="middle"
    fill="var(--text-muted)"
    font-size="8"
  >
    {label}
  </text>
</g>

<style>
  .container-node {
    cursor: pointer;
  }

  .container-node:hover circle {
    filter: brightness(1.4);
  }

  .select-ring {
    animation: select-glow 1.5s ease-in-out infinite;
  }

  @keyframes select-glow {
    0%, 100% { opacity: 0.5; }
    50% { opacity: 0.25; }
  }

  .pulse {
    animation: container-pulse 2s ease-in-out infinite;
  }

  @keyframes container-pulse {
    0%, 100% {
      r: 14;
      opacity: 1;
    }
    50% {
      r: 16;
      opacity: 0.7;
    }
  }
</style>

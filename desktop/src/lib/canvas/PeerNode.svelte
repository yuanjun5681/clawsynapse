<script lang="ts">
  import type { PeerInfo } from "./CanvasView.svelte";

  interface Props {
    x: number;
    y: number;
    info: PeerInfo;
    selected?: boolean;
  }

  let { x, y, info, selected = false }: Props = $props();
  let isOnline = $derived(info.status === "online");
</script>

<g class="peer-node">
  <!-- Selection ring -->
  {#if selected}
    <circle cx={x} cy={y} r="28" fill="none" stroke={isOnline ? "var(--green)" : "var(--text-muted)"} stroke-width="2" opacity="0.5" class="select-ring" />
  {/if}
  <!-- Circle -->
  <circle
    cx={x}
    cy={y}
    r="22"
    fill="#111"
    stroke={isOnline ? "var(--green)" : "var(--text-muted)"}
    stroke-width="1.5"
    stroke-dasharray={isOnline ? "none" : "4 3"}
  />

  <!-- Status dot -->
  <circle
    cx={x + 16}
    cy={y - 16}
    r="4"
    fill={isOnline ? "var(--green)" : "var(--text-muted)"}
  />

  <!-- Icon -->
  <text
    {x}
    y={y + 1}
    text-anchor="middle"
    dominant-baseline="central"
    fill={isOnline ? "var(--green)" : "var(--text-muted)"}
    font-size="14"
  >
    P
  </text>

  <!-- Name -->
  <text
    {x}
    y={y + 34}
    text-anchor="middle"
    fill="var(--text)"
    font-size="10"
  >
    {info.name || info.id.slice(0, 8)}
  </text>
</g>

<style>
  .peer-node {
    cursor: pointer;
  }

  .peer-node:hover circle {
    filter: brightness(1.3);
  }

  .select-ring {
    animation: select-glow 1.5s ease-in-out infinite;
  }

  @keyframes select-glow {
    0%, 100% { opacity: 0.5; }
    50% { opacity: 0.25; }
  }
</style>

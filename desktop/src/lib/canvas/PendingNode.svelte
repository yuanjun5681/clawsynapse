<script lang="ts">
  import type { PendingPeerInfo } from "./CanvasView.svelte";

  interface Props {
    x: number;
    y: number;
    info: PendingPeerInfo;
  }

  let { x, y, info }: Props = $props();
</script>

<g class="pending-node">
  <!-- Dashed circle -->
  <circle
    cx={x}
    cy={y}
    r="18"
    fill="none"
    stroke="var(--yellow)"
    stroke-width="1.5"
    stroke-dasharray="5 3"
    class="pending-ring"
  />

  <!-- Question mark icon -->
  <text
    {x}
    y={y + 1}
    text-anchor="middle"
    dominant-baseline="central"
    fill="var(--yellow)"
    font-size="14"
  >
    ?
  </text>

  <!-- Name -->
  <text
    {x}
    y={y + 30}
    text-anchor="middle"
    fill="var(--yellow)"
    font-size="9"
    opacity="0.8"
  >
    {info.name || info.id.slice(0, 8)}
  </text>
</g>

<style>
  .pending-ring {
    animation: rotate-dash 4s linear infinite;
  }

  @keyframes rotate-dash {
    0% {
      stroke-dashoffset: 0;
    }
    100% {
      stroke-dashoffset: 50;
    }
  }
</style>

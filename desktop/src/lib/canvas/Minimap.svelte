<script lang="ts">
  interface Props {
    nodes: Array<{ x: number; y: number; type: string }>;
    viewTx: number;
    viewTy: number;
    viewZoom: number;
    canvasW: number;
    canvasH: number;
  }

  let { nodes, viewTx, viewTy, viewZoom, canvasW, canvasH }: Props = $props();

  const MINIMAP_W = 120;
  const MINIMAP_H = 80;
  const PADDING = 20;

  // Compute bounding box of all nodes
  let bounds = $derived.by(() => {
    if (nodes.length === 0) return { minX: 0, minY: 0, maxX: 100, maxY: 100 };
    let minX = Infinity,
      minY = Infinity,
      maxX = -Infinity,
      maxY = -Infinity;
    for (const n of nodes) {
      if (n.x < minX) minX = n.x;
      if (n.y < minY) minY = n.y;
      if (n.x > maxX) maxX = n.x;
      if (n.y > maxY) maxY = n.y;
    }
    // Add margin
    const margin = 50;
    return {
      minX: minX - margin,
      minY: minY - margin,
      maxX: maxX + margin,
      maxY: maxY + margin,
    };
  });

  let worldW = $derived(bounds.maxX - bounds.minX || 1);
  let worldH = $derived(bounds.maxY - bounds.minY || 1);
  let scale = $derived(Math.min(MINIMAP_W / worldW, MINIMAP_H / worldH));

  function worldToMini(wx: number, wy: number): { x: number; y: number } {
    return {
      x: (wx - bounds.minX) * scale,
      y: (wy - bounds.minY) * scale,
    };
  }

  // Viewport rect in minimap
  let viewRect = $derived.by(() => {
    const vx = (canvasW / 2 - canvasW / 2 / viewZoom - viewTx);
    const vy = (canvasH / 2 - canvasH / 2 / viewZoom - viewTy);
    const vw = canvasW / viewZoom;
    const vh = canvasH / viewZoom;
    const topLeft = worldToMini(vx, vy);
    return {
      x: topLeft.x,
      y: topLeft.y,
      width: vw * scale,
      height: vh * scale,
    };
  });

  let colorMap: Record<string, string> = {
    local: "var(--accent)",
    peer: "var(--green)",
    pending: "var(--yellow)",
    container: "#4488ff",
  };
</script>

<div class="minimap" style="width:{MINIMAP_W}px; height:{MINIMAP_H}px;">
  <svg width={MINIMAP_W} height={MINIMAP_H}>
    <!-- Background -->
    <rect width={MINIMAP_W} height={MINIMAP_H} fill="#080808" rx="4" opacity="0.8" />

    <!-- Nodes as dots -->
    {#each nodes as node}
      {@const pos = worldToMini(node.x, node.y)}
      <circle
        cx={pos.x}
        cy={pos.y}
        r={node.type === "local" ? 3 : 1.5}
        fill={colorMap[node.type] || "var(--text-muted)"}
      />
    {/each}

    <!-- Viewport indicator -->
    <rect
      x={viewRect.x}
      y={viewRect.y}
      width={viewRect.width}
      height={viewRect.height}
      fill="none"
      stroke="var(--text-muted)"
      stroke-width="0.5"
      opacity="0.5"
    />
  </svg>
</div>

<style>
  .minimap {
    position: absolute;
    bottom: 12px;
    right: 12px;
    border: 1px solid rgba(var(--accent-rgb), 0.2);
    border-radius: 2px;
    overflow: hidden;
    pointer-events: none;
    opacity: 0.7;
    box-shadow: 0 0 var(--glow-spread) rgba(var(--accent-rgb), calc(var(--glow-opacity) * 0.15));
  }

  .minimap::before {
    content: '';
    position: absolute;
    top: -1px;
    left: -1px;
    width: 8px;
    height: 8px;
    border-top: 1px solid var(--accent);
    border-left: 1px solid var(--accent);
    opacity: var(--glow-opacity);
    z-index: 1;
  }

  .minimap::after {
    content: '';
    position: absolute;
    bottom: -1px;
    right: -1px;
    width: 8px;
    height: 8px;
    border-bottom: 1px solid var(--accent);
    border-right: 1px solid var(--accent);
    opacity: var(--glow-opacity);
    z-index: 1;
  }
</style>

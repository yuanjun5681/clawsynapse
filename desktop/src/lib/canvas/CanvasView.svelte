<script lang="ts">
  import { onMount } from "svelte";

  import LocalNode from "./LocalNode.svelte";
  import PeerNode from "./PeerNode.svelte";
  import PendingNode from "./PendingNode.svelte";
  import ContainerNode from "./ContainerNode.svelte";
  import TaskNode from "./TaskNode.svelte";
  import Edge from "./Edge.svelte";
  import Minimap from "./Minimap.svelte";

  interface Props {
    status: MonitorStatus | null;
    containers: ContainerInfo[];
    tasks: TaskInfo[];
    pilot: PilotInfo | null;
    selectedId: string | null;
    onSelectLocal: () => void;
    onSelectPeer: (peer: PeerInfo) => void;
    onSelectContainer: (container: ContainerInfo) => void;
    onCanvasClick?: () => void;
  }

  export interface MonitorStatus {
    uptime: number;
    memoryMB: number;
    activeContainers: number;
    maxContainers: number;
    waitingGroups: number;
    registeredGroups: number;
    pid: number;
  }

  export interface ContainerInfo {
    name: string;
    status: string;
    created: string;
    image: string;
    groupFolder: string | null;
  }

  export interface TaskInfo {
    id: string;
    group_folder: string;
    prompt: string;
    schedule_type: string;
    schedule_value: string;
    status: string;
    next_run: string | null;
  }

  export interface PeerInfo {
    id: string;
    name: string;
    address: string;
    status: "online" | "offline";
  }

  export interface PendingPeerInfo {
    id: string;
    name: string;
    justification?: string;
  }

  export interface PilotInfo {
    available: boolean;
    node?: Record<string, unknown>;
    trustedPeers: PeerInfo[];
    pendingHandshakes: PendingPeerInfo[];
  }

  let {
    status,
    containers,
    tasks,
    pilot,
    selectedId,
    onSelectLocal,
    onSelectPeer,
    onSelectContainer,
    onCanvasClick,
  }: Props = $props();

  let svgEl: SVGSVGElement;
  let w = $state(800);
  let h = $state(600);

  // Pan/Zoom state
  let tx = $state(0);
  let ty = $state(0);
  let zoom = $state(1);
  let isPanning = $state(false);
  let panStartX = 0;
  let panStartY = 0;
  let panStartTx = 0;
  let panStartTy = 0;

  // Computed center
  let cx = $derived(w / 2);
  let cy = $derived(h / 2);

  // Layout radii
  const R0 = 90; // containers orbit radius
  const R0_TASK = 130; // tasks orbit radius
  const R1 = 240; // peers orbit radius

  // Derived peer data
  let trustedPeers = $derived(pilot?.trustedPeers ?? []);
  let pendingPeers = $derived(pilot?.pendingHandshakes ?? []);
  let activeTasks = $derived(
    tasks.filter((t) => t.status === "active" || t.status === "running")
  );

  // Peer positions
  function peerPosition(index: number, total: number): { x: number; y: number } {
    if (total === 0) return { x: cx, y: cy };
    const angle = (index * 2 * Math.PI) / total - Math.PI / 2;
    return {
      x: cx + R1 * Math.cos(angle),
      y: cy + R1 * Math.sin(angle),
    };
  }

  // Container positions
  function containerPosition(
    index: number,
    total: number
  ): { x: number; y: number } {
    if (total === 0) return { x: cx, y: cy };
    const offset = Math.PI / 4; // offset from 12 o'clock
    const angle = (index * 2 * Math.PI) / total + offset - Math.PI / 2;
    return {
      x: cx + R0 * Math.cos(angle),
      y: cy + R0 * Math.sin(angle),
    };
  }

  // Task positions
  function taskPosition(
    index: number,
    total: number
  ): { x: number; y: number } {
    if (total === 0) return { x: cx, y: cy };
    const offset = -Math.PI / 6;
    const angle = (index * 2 * Math.PI) / total + offset - Math.PI / 2;
    return {
      x: cx + R0_TASK * Math.cos(angle),
      y: cy + R0_TASK * Math.sin(angle),
    };
  }

  // Pending positions (beyond peers)
  function pendingPosition(
    index: number,
    total: number
  ): { x: number; y: number } {
    if (total === 0) return { x: cx, y: cy };
    const R_PENDING = R1 + 80;
    const offset = Math.PI / 3;
    const angle = (index * 2 * Math.PI) / total + offset - Math.PI / 2;
    return {
      x: cx + R_PENDING * Math.cos(angle),
      y: cy + R_PENDING * Math.sin(angle),
    };
  }

  // All node positions for minimap
  let allNodes = $derived.by(() => {
    const nodes: Array<{ x: number; y: number; type: string }> = [
      { x: cx, y: cy, type: "local" },
    ];
    const peerTotal = trustedPeers.length;
    trustedPeers.forEach((_, i) => {
      const p = peerPosition(i, peerTotal);
      nodes.push({ ...p, type: "peer" });
    });
    pendingPeers.forEach((_, i) => {
      const p = pendingPosition(i, pendingPeers.length);
      nodes.push({ ...p, type: "pending" });
    });
    containers.forEach((_, i) => {
      const p = containerPosition(i, containers.length);
      nodes.push({ ...p, type: "container" });
    });
    return nodes;
  });

  // Pan handlers
  function handlePointerDown(e: PointerEvent) {
    if (e.button !== 0) return;
    // Don't start pan if clicking on a node
    const target = e.target as SVGElement;
    if (target.closest("[data-node]")) return;

    isPanning = true;
    panStartX = e.clientX;
    panStartY = e.clientY;
    panStartTx = tx;
    panStartTy = ty;
    svgEl.setPointerCapture(e.pointerId);
  }

  function handlePointerMove(e: PointerEvent) {
    if (!isPanning) return;
    tx = panStartTx + (e.clientX - panStartX) / zoom;
    ty = panStartTy + (e.clientY - panStartY) / zoom;
  }

  function handlePointerUp(e: PointerEvent) {
    if (isPanning) {
      const dx = e.clientX - panStartX;
      const dy = e.clientY - panStartY;
      isPanning = false;
      svgEl.releasePointerCapture(e.pointerId);
      // If barely moved, treat as click on blank canvas
      if (Math.abs(dx) < 4 && Math.abs(dy) < 4) {
        onCanvasClick?.();
      }
    }
  }

  function handleWheel(e: WheelEvent) {
    e.preventDefault();
    const factor = e.deltaY > 0 ? 0.9 : 1.1;
    const newZoom = Math.max(0.2, Math.min(3, zoom * factor));

    // Zoom toward cursor position
    const rect = svgEl.getBoundingClientRect();
    const mx = e.clientX - rect.left;
    const my = e.clientY - rect.top;

    // Point in world space before zoom
    const wx = (mx - w / 2) / zoom - tx + w / 2;
    const wy = (my - h / 2) / zoom - ty + h / 2;

    zoom = newZoom;

    // Adjust tx, ty so the point under cursor stays fixed
    tx = -(wx - w / 2) + (mx - w / 2) / zoom;
    ty = -(wy - h / 2) + (my - h / 2) / zoom;
  }

  // Fit view to center on mount
  onMount(() => {
    tx = 0;
    ty = 0;
    zoom = 1;
  });
</script>

<div class="canvas-wrapper">
  <svg
    bind:this={svgEl}
    bind:clientWidth={w}
    bind:clientHeight={h}
    onwheel={handleWheel}
    onpointerdown={handlePointerDown}
    onpointermove={handlePointerMove}
    onpointerup={handlePointerUp}
    class:panning={isPanning}
  >
    <g transform="translate({w / 2 + tx * zoom - w / 2},{h / 2 + ty * zoom - h / 2}) scale({zoom})">
      <!-- Edges: local to peers -->
      {#each trustedPeers as peer, i}
        {@const pos = peerPosition(i, trustedPeers.length)}
        <Edge
          x1={cx}
          y1={cy}
          x2={pos.x}
          y2={pos.y}
          status={peer.status}
          id="edge-peer-{peer.id}"
        />
      {/each}

      <!-- Edges: local to pending -->
      {#each pendingPeers as _peer, i}
        {@const pos = pendingPosition(i, pendingPeers.length)}
        <Edge
          x1={cx}
          y1={cy}
          x2={pos.x}
          y2={pos.y}
          status="pending"
          id="edge-pending-{i}"
        />
      {/each}

      <!-- Edges: local to containers (thin lines) -->
      {#each containers as _c, i}
        {@const pos = containerPosition(i, containers.length)}
        <line
          x1={cx}
          y1={cy}
          x2={pos.x}
          y2={pos.y}
          stroke="var(--border)"
          stroke-width="1"
          opacity="0.3"
        />
      {/each}

      <!-- Task thin connectors -->
      {#each activeTasks as _t, i}
        {@const pos = taskPosition(i, activeTasks.length)}
        <line
          x1={cx}
          y1={cy}
          x2={pos.x}
          y2={pos.y}
          stroke="var(--border)"
          stroke-width="1"
          opacity="0.2"
          stroke-dasharray="4 4"
        />
      {/each}

      <!-- Container nodes -->
      {#each containers as container, i}
        {@const pos = containerPosition(i, containers.length)}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <g data-node onclick={(e: MouseEvent) => { e.stopPropagation(); onSelectContainer(container); }}>
          <ContainerNode x={pos.x} y={pos.y} info={container} selected={selectedId === container.name} />
        </g>
      {/each}

      <!-- Task nodes -->
      {#each activeTasks as task, i}
        {@const pos = taskPosition(i, activeTasks.length)}
        <TaskNode x={pos.x} y={pos.y} info={task} />
      {/each}

      <!-- Peer nodes -->
      {#each trustedPeers as peer, i}
        {@const pos = peerPosition(i, trustedPeers.length)}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <g data-node onclick={(e: MouseEvent) => { e.stopPropagation(); onSelectPeer(peer); }}>
          <PeerNode x={pos.x} y={pos.y} info={peer} selected={selectedId === peer.id} />
        </g>
      {/each}

      <!-- Pending nodes -->
      {#each pendingPeers as peer, i}
        {@const pos = pendingPosition(i, pendingPeers.length)}
        <PendingNode x={pos.x} y={pos.y} info={peer} />
      {/each}

      <!-- Local node (center) -->
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <g data-node onclick={(e: MouseEvent) => { e.stopPropagation(); onSelectLocal(); }}>
        <LocalNode x={cx} y={cy} {status} containerCount={containers.length} selected={selectedId === 'local'} />
      </g>
    </g>

    <!-- Pilot unavailable overlay -->
    {#if pilot && !pilot.available}
      <text
        x={w - 16}
        y={28}
        text-anchor="end"
        fill="var(--text-muted)"
        font-size="11"
        opacity="0.6"
      >
        Pilot 未连接
      </text>
    {/if}
  </svg>

  <Minimap nodes={allNodes} viewTx={tx} viewTy={ty} viewZoom={zoom} canvasW={w} canvasH={h} />
</div>

<style>
  .canvas-wrapper {
    position: relative;
    width: 100%;
    height: 100%;
    overflow: hidden;
  }

  svg {
    width: 100%;
    height: 100%;
    cursor: grab;
    user-select: none;
  }

  svg.panning {
    cursor: grabbing;
  }
</style>

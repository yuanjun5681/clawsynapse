<script lang="ts">
  import type { NodeEventKind } from '../domain/pilot/pilot-events';
  import type { PeerInfo } from './CanvasView.svelte';

  interface Props {
    x: number;
    y: number;
    info: PeerInfo;
    selected?: boolean;
    unreadCount?: number;
    lastEventKind?: NodeEventKind | null;
  }

  let {
    x,
    y,
    info,
    selected = false,
    unreadCount = 0,
    lastEventKind = null,
  }: Props = $props();

  let isOnline = $derived(info.status === 'online');

  function eventBadge(kind: NodeEventKind | null): string {
    if (kind === 'message.received') return 'M';
    if (kind === 'message.sent') return 'S';
    if (kind === 'data.file') return 'F';
    if (kind === 'handshake.received') return 'H';
    return '';
  }
</script>

<g class="peer-node">
  {#if selected}
    <circle cx={x} cy={y} r="28" fill="none" stroke={isOnline ? 'var(--green)' : 'var(--text-muted)'} stroke-width="2" opacity="0.5" class="select-ring" />
  {/if}

  <circle
    cx={x}
    cy={y}
    r="22"
    fill="#111"
    stroke={isOnline ? 'var(--green)' : 'var(--text-muted)'}
    stroke-width="1.5"
    stroke-dasharray={isOnline ? 'none' : '4 3'}
    class:has-unread={unreadCount > 0}
  />

  <circle
    cx={x + 16}
    cy={y - 16}
    r="4"
    fill={isOnline ? 'var(--green)' : 'var(--text-muted)'}
  />

  {#if unreadCount > 0}
    <g>
      <circle cx={x - 16} cy={y - 16} r="8" fill="var(--red)" opacity="0.95" />
      <text
        x={x - 16}
        y={y - 15}
        text-anchor="middle"
        dominant-baseline="central"
        fill="#fff"
        font-size="8"
        font-weight="700"
      >
        {unreadCount > 99 ? '99+' : unreadCount}
      </text>
    </g>
  {/if}

  {#if eventBadge(lastEventKind)}
    <g>
      <rect x={x + 10} y={y + 10} width="12" height="12" rx="3" ry="3" fill="var(--blue)" opacity="0.9" />
      <text
        x={x + 16}
        y={y + 16}
        text-anchor="middle"
        dominant-baseline="central"
        fill="#fff"
        font-size="8"
        font-weight="600"
      >
        {eventBadge(lastEventKind)}
      </text>
    </g>
  {/if}

  <text
    {x}
    y={y + 1}
    text-anchor="middle"
    dominant-baseline="central"
    fill={isOnline ? 'var(--green)' : 'var(--text-muted)'}
    font-size="14"
  >
    P
  </text>

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

  circle.has-unread {
    animation: unread-pulse 1.6s ease-in-out infinite;
  }

  @keyframes select-glow {
    0%, 100% { opacity: 0.5; }
    50% { opacity: 0.25; }
  }

  @keyframes unread-pulse {
    0%, 100% { filter: drop-shadow(0 0 0 rgba(255, 0, 0, 0)); }
    50% { filter: drop-shadow(0 0 6px rgba(255, 0, 0, 0.75)); }
  }
</style>

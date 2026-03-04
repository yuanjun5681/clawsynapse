<script lang="ts">
  import type { NodeEvent } from '../domain/pilot/pilot-events';
  import { pilotEventStore } from '../state/pilotEventStore';
  import { monitorStreamStore } from '../state/monitorStreamStore';

  const MAX_EVENTS = 50;

  let events = $derived($pilotEventStore.globalRecentEvents.slice(0, MAX_EVENTS));
  let connected = $derived($monitorStreamStore.connected);

  function formatTime(ts: string): string {
    try {
      return new Date(ts).toLocaleTimeString();
    } catch {
      return ts;
    }
  }

  function eventLabel(event: NodeEvent): string {
    if (event.kind === 'message.received') return 'Message received';
    if (event.kind === 'data.file') return 'File received';
    if (event.kind === 'handshake.received') return 'Handshake request';
    return 'Pilot event';
  }

  function eventColor(event: NodeEvent): string {
    if (event.severity === 'warn') return 'var(--yellow)';
    if (event.kind === 'data.file') return 'var(--blue)';
    return 'var(--green)';
  }

  function extractMessageText(event: NodeEvent): string {
    const data = event.raw.data as Record<string, unknown> | undefined;
    if (!data) return '';
    for (const key of ['content', 'message', 'text', 'body', 'value']) {
      const value = data[key];
      if (typeof value === 'string' && value.trim().length > 0) {
        return value;
      }
    }
    return '';
  }

  function eventDetail(event: NodeEvent): string {
    if (event.kind === 'message.received') {
      return extractMessageText(event) || '(no message content)';
    }
    return event.summary;
  }
</script>

<div class="activity-feed">
  <div class="corner corner-tl"></div>
  <div class="corner corner-tr"></div>
  <div class="corner corner-bl"></div>
  <div class="corner corner-br"></div>
  <div class="scanline-overlay"></div>

  <div class="feed-header">
    <span class="feed-title">Activity</span>
    <span class="feed-counter">{events.length}</span>
    <span class="feed-status" class:connected></span>
  </div>

  {#if events.length === 0}
    <div class="feed-empty">No recent activity</div>
  {:else}
    <div class="feed-list">
      {#each events as ev}
        <div class="feed-item">
          <span class="feed-dot" style="background:{eventColor(ev)}"></span>
          <div class="feed-content">
            <span class="feed-label">{eventLabel(ev)}</span>
            <span class="feed-detail">node {ev.nodeIdForCanvas}</span>
            <span class="feed-detail">{eventDetail(ev)}</span>
          </div>
          <span class="feed-time">{formatTime(ev.ts)}</span>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .activity-feed {
    padding: 8px;
    border: 1px solid var(--tron-border-color, rgba(var(--accent-rgb), 0.2));
    border-radius: var(--border-radius, 4px);
    position: relative;
    overflow: hidden;
    transition: border-color 0.25s ease-out, box-shadow 0.25s ease-out;
  }

  .corner {
    position: absolute;
    width: 8px;
    height: 8px;
    z-index: 1;
  }

  .corner-tl {
    top: -1px;
    left: -1px;
    border-top: 1px solid var(--accent);
    border-left: 1px solid var(--accent);
  }

  .corner-tr {
    top: -1px;
    right: -1px;
    border-top: 1px solid var(--accent);
    border-right: 1px solid var(--accent);
  }

  .corner-bl {
    bottom: -1px;
    left: -1px;
    border-bottom: 1px solid var(--accent);
    border-left: 1px solid var(--accent);
  }

  .corner-br {
    bottom: -1px;
    right: -1px;
    border-bottom: 1px solid var(--accent);
    border-right: 1px solid var(--accent);
  }

  .scanline-overlay {
    position: absolute;
    inset: 0;
    background: repeating-linear-gradient(
      0deg,
      transparent,
      transparent 2px,
      rgba(0, 0, 0, 0.03) 2px,
      rgba(0, 0, 0, 0.03) 4px
    );
    pointer-events: none;
    z-index: 2;
  }

  .feed-header {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 4px 0 8px;
  }

  .feed-title {
    font-size: 10px;
    font-weight: 600;
    color: var(--text);
    text-transform: uppercase;
    letter-spacing: 0.1em;
    font-family: var(--font-display);
  }

  .feed-counter {
    font-size: 9px;
    color: var(--text-muted);
    margin-left: auto;
  }

  .feed-status {
    width: 5px;
    height: 5px;
    border-radius: 50%;
    background: var(--red);
  }

  .feed-status.connected {
    background: var(--green);
    animation: pulse-dot 2s ease-in-out infinite;
  }

  @keyframes pulse-dot {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.4; }
  }

  .feed-empty {
    color: var(--text-muted);
    font-size: 11px;
    padding: 8px 0;
  }

  .feed-list {
    display: flex;
    flex-direction: column;
    max-height: 200px;
    overflow-y: auto;
  }

  .feed-item {
    display: flex;
    align-items: flex-start;
    gap: 6px;
    padding: 4px 0;
    font-size: 10px;
    border-bottom: 1px solid rgba(var(--accent-rgb), 0.08);
  }

  .feed-item:last-child {
    border-bottom: none;
  }

  .feed-dot {
    width: 4px;
    height: 4px;
    border-radius: 50%;
    flex-shrink: 0;
    margin-top: 4px;
  }

  .feed-content {
    display: flex;
    flex-direction: column;
    min-width: 0;
    flex: 1;
    gap: 2px;
  }

  .feed-label {
    color: var(--text);
  }

  .feed-detail {
    color: var(--text-muted);
    line-height: 1.3;
    word-break: break-word;
  }

  .feed-time {
    color: var(--text-muted);
    flex-shrink: 0;
  }
</style>

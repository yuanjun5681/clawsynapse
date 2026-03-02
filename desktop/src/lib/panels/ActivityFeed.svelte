<script lang="ts">
  import { onMount } from "svelte";
  import { getMonitorEventsUrl } from "../api";

  interface MonitorEvent {
    type: string;
    timestamp: string;
    data: Record<string, unknown>;
  }

  let events = $state<MonitorEvent[]>([]);
  let connected = $state(false);
  const MAX_EVENTS = 50;

  function addEvent(ev: MonitorEvent) {
    events = [ev, ...events].slice(0, MAX_EVENTS);
  }

  function formatTime(ts: string): string {
    try {
      return new Date(ts).toLocaleTimeString();
    } catch {
      return ts;
    }
  }

  function eventLabel(type: string): string {
    switch (type) {
      case 'container.start': return 'Container started';
      case 'container.stop': return 'Container stopped';
      case 'task.run': return 'Task running';
      case 'task.complete': return 'Task complete';
      case 'pilot.message': return 'Pilot message';
      case 'ipc.activity': return 'IPC activity';
      default: return type;
    }
  }

  function eventColor(type: string): string {
    if (type.startsWith('container.start')) return 'var(--green)';
    if (type.startsWith('container.stop')) return 'var(--text-muted)';
    if (type.startsWith('task.run')) return 'var(--accent)';
    if (type.startsWith('task.complete')) return 'var(--green)';
    if (type.startsWith('pilot')) return 'var(--blue)';
    return 'var(--text-muted)';
  }

  onMount(() => {
    let eventSource: EventSource | null = null;

    function connect() {
      eventSource = new EventSource(getMonitorEventsUrl());

      eventSource.onopen = () => {
        connected = true;
      };

      eventSource.onerror = () => {
        connected = false;
        eventSource?.close();
        // Reconnect after 5s
        setTimeout(connect, 5000);
      };

      // Listen to all event types
      const eventTypes = [
        'container.start',
        'container.stop',
        'task.run',
        'task.complete',
        'pilot.message',
        'ipc.activity',
      ];

      for (const type of eventTypes) {
        eventSource.addEventListener(type, (e: MessageEvent) => {
          try {
            const data = JSON.parse(e.data) as MonitorEvent;
            addEvent(data);
          } catch { /* ignore */ }
        });
      }
    }

    connect();

    return () => {
      eventSource?.close();
    };
  });
</script>

<div class="activity-feed">
  <div class="feed-header">
    <span class="feed-title">Activity</span>
    <span class="feed-status" class:connected></span>
  </div>

  {#if events.length === 0}
    <div class="feed-empty">No recent activity</div>
  {:else}
    <div class="feed-list">
      {#each events as ev}
        <div class="feed-item">
          <span class="feed-dot" style="background:{eventColor(ev.type)}"></span>
          <div class="feed-content">
            <span class="feed-label">{eventLabel(ev.type)}</span>
            {#if ev.data.groupFolder}
              <span class="feed-detail">{ev.data.groupFolder}</span>
            {/if}
            {#if ev.data.containerName}
              <span class="feed-detail">{ev.data.containerName}</span>
            {/if}
          </div>
          <span class="feed-time">{formatTime(ev.timestamp)}</span>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .activity-feed {
    padding: 0;
  }

  .feed-header {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 8px 0;
  }

  .feed-title {
    font-size: 12px;
    font-weight: 600;
    color: var(--text);
  }

  .feed-status {
    width: 5px;
    height: 5px;
    border-radius: 50%;
    background: var(--red);
  }

  .feed-status.connected {
    background: var(--green);
  }

  .feed-empty {
    color: var(--text-muted);
    font-size: 11px;
    padding: 8px 0;
  }

  .feed-list {
    display: flex;
    flex-direction: column;
    gap: 2px;
    max-height: 200px;
    overflow-y: auto;
  }

  .feed-item {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 3px 0;
    font-size: 10px;
  }

  .feed-dot {
    width: 4px;
    height: 4px;
    border-radius: 50%;
    flex-shrink: 0;
  }

  .feed-content {
    flex: 1;
    display: flex;
    gap: 4px;
    align-items: center;
    min-width: 0;
  }

  .feed-label {
    color: var(--text);
    white-space: nowrap;
  }

  .feed-detail {
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .feed-time {
    color: var(--text-muted);
    flex-shrink: 0;
    font-size: 9px;
  }
</style>

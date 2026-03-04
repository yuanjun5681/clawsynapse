<script lang="ts">
  import { actOnPilotHandshake, fetchMonitorPilotInbox } from '../api';
  import {
    canonicalizeNodeId,
    type NodeEvent,
    type NodeEventKind,
  } from '../domain/pilot/pilot-events';
  import type { PeerInfo } from '../canvas/CanvasView.svelte';
  import { pilotEventStore } from '../state/pilotEventStore';

  interface Props {
    peer: PeerInfo;
  }

  type EventFilter = 'all' | NodeEventKind;

  let { peer }: Props = $props();

  let filter = $state<EventFilter>('all');
  let handshakeActionInFlight = $state<string | null>(null);
  let handshakeError = $state<string | null>(null);
  let handshakeSuccess = $state<string | null>(null);
  let canonicalPeerId = $derived(canonicalizeNodeId(peer.id));
  interface InboxMessage {
    from: string;
    content: string;
    timestamp: string;
    type?: string;
    raw?: Record<string, unknown>;
  }
  let inboxMessages = $state<InboxMessage[]>([]);

  let nodeEvents = $derived($pilotEventStore.eventsByNodeId[canonicalPeerId] ?? []);
  let filteredEvents = $derived(
    filter === 'all'
      ? nodeEvents
      : nodeEvents.filter((event) => event.kind === filter)
  );

  $effect(() => {
    pilotEventStore.markNodeRead(canonicalPeerId);
  });

  $effect(() => {
    let cancelled = false;
    const refreshKey = nodeEvents[0]?.id ?? '';

    const loadInbox = () => {
      fetchMonitorPilotInbox()
        .then((payload) => {
          if (cancelled) return;
          const filtered = (payload.messages ?? [])
            .filter((msg) => {
              const from = String(msg.from ?? '');
              const fromCanonical = canonicalizeNodeId(from);
              if (fromCanonical === canonicalPeerId) return true;
              if (peer.address && from === peer.address) return true;
              return false;
            })
            .filter((msg) => typeof msg.content === 'string' && msg.content.trim().length > 0)
            .sort((a, b) => b.timestamp.localeCompare(a.timestamp));
          inboxMessages = filtered;
        })
        .catch(() => {
          if (cancelled) return;
          inboxMessages = [];
        });
    };

    loadInbox();
    const timer = setInterval(loadInbox, 3000);

    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  });

  function formatTime(ts: string): string {
    try {
      return new Date(ts).toLocaleString();
    } catch {
      return ts;
    }
  }

  function kindLabel(kind: NodeEventKind): string {
    if (kind === 'message.received') return 'Message';
    if (kind === 'data.file') return 'File';
    if (kind === 'handshake.received') return 'Handshake';
    return 'Unknown';
  }

  function resolveHandshakeNodeId(event: NodeEvent): string {
    return event.peerNodeId ?? event.nodeIdForCanvas;
  }

  function extractRawMessageText(event: NodeEvent): string {
    const data = event.raw.data as Record<string, unknown> | undefined;
    if (!data) return '';

    for (const key of ['message', 'content', 'text', 'body', 'value']) {
      const value = data[key];
      if (typeof value === 'string' && value.trim().length > 0) {
        return value;
      }
    }

    return '';
  }

  function secondBucket(ts: string): string {
    const d = new Date(ts);
    if (!Number.isNaN(d.getTime())) {
      return d.toISOString().slice(0, 19);
    }
    return ts.slice(0, 19);
  }

  function resolveMessageDisplay(event: NodeEvent): string {
    if (event.kind !== 'message.received') return event.summary;

    const rawText = extractRawMessageText(event);
    if (rawText) {
      return rawText;
    }

    const eventBucket = secondBucket(event.ts);
    const bucketMatch = inboxMessages.find(
      (msg) => secondBucket(msg.timestamp) === eventBucket,
    );
    if (bucketMatch) {
      return bucketMatch.content;
    }

    if (inboxMessages.length > 0) {
      return inboxMessages[0].content;
    }

    return '(no message content)';
  }

  async function handleHandshakeAction(event: NodeEvent, action: 'approve' | 'reject'): Promise<void> {
    const nodeId = resolveHandshakeNodeId(event);
    if (!nodeId || nodeId === 'unknown') {
      handshakeError = 'Cannot resolve node id for handshake action';
      return;
    }

    handshakeError = null;
    handshakeSuccess = null;
    handshakeActionInFlight = `${action}:${event.id}`;
    try {
      await actOnPilotHandshake(nodeId, action);
      handshakeSuccess = `${action} succeeded for node ${nodeId}`;
    } catch (error) {
      handshakeError = error instanceof Error ? error.message : 'Handshake action failed';
    } finally {
      handshakeActionInFlight = null;
    }
  }

</script>

<div class="peer-detail">
  <div class="section">
    <div class="section-title">
      <span class="status-dot" class:online={peer.status === 'online'}></span>
      {peer.name || peer.id.slice(0, 12)}
    </div>

    <div class="info-grid">
      <div class="info-row">
        <span class="info-label">ID</span>
        <span class="info-value">{peer.id}</span>
      </div>
      {#if peer.address}
        <div class="info-row">
          <span class="info-label">Address</span>
          <span class="info-value">{peer.address}</span>
        </div>
      {/if}
      <div class="info-row">
        <span class="info-label">Status</span>
        <span class="info-value" class:online={peer.status === 'online'}>
          {peer.status}
        </span>
      </div>
    </div>
  </div>

  <div class="divider"></div>

  <div class="section">
    <div class="section-title">Events</div>
    {#if handshakeError}
      <div class="feedback error">{handshakeError}</div>
    {/if}
    {#if handshakeSuccess}
      <div class="feedback success">{handshakeSuccess}</div>
    {/if}
    <div class="filters">
      <button class:active={filter === 'all'} onclick={() => { filter = 'all'; }}>All</button>
      <button class:active={filter === 'message.received'} onclick={() => { filter = 'message.received'; }}>Message</button>
      <button class:active={filter === 'data.file'} onclick={() => { filter = 'data.file'; }}>File</button>
      <button class:active={filter === 'handshake.received'} onclick={() => { filter = 'handshake.received'; }}>Handshake</button>
    </div>

    {#if filteredEvents.length === 0}
      <div class="placeholder">No events for this peer</div>
    {:else}
      <div class="message-list">
        {#each filteredEvents as event}
          <div class="message-item" class:warn={event.severity === 'warn'} class:info={event.severity !== 'warn'}>
            <div class="message-meta">
              <span class="message-from">{kindLabel(event.kind)}</span>
              <span class="message-time">{formatTime(event.ts)}</span>
            </div>
            <div class="message-content">{resolveMessageDisplay(event)}</div>
            {#if event.kind === 'handshake.received'}
              <div class="message-actions">
                <button
                  disabled={handshakeActionInFlight !== null}
                  onclick={() => { handleHandshakeAction(event, 'approve'); }}
                >
                  {handshakeActionInFlight === `approve:${event.id}` ? 'Approving...' : 'Approve'}
                </button>
                <button
                  disabled={handshakeActionInFlight !== null}
                  onclick={() => { handleHandshakeAction(event, 'reject'); }}
                >
                  {handshakeActionInFlight === `reject:${event.id}` ? 'Rejecting...' : 'Reject'}
                </button>
              </div>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>

<style>
  .peer-detail {
    display: flex;
    flex-direction: column;
  }

  .section {
    padding: 12px 16px;
  }

  .section-title {
    font-size: 12px;
    font-weight: 600;
    color: var(--text);
    margin-bottom: 8px;
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .status-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-muted);
  }

  .status-dot.online {
    background: var(--green);
  }

  .info-grid {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .info-row {
    display: flex;
    justify-content: space-between;
    font-size: 11px;
  }

  .info-label {
    color: var(--text-muted);
  }

  .info-value {
    color: var(--text);
    text-align: right;
    word-break: break-all;
    max-width: 200px;
  }

  .info-value.online {
    color: var(--green);
  }

  .divider {
    height: 1px;
    background: var(--border);
    margin: 0 16px;
  }

  .filters {
    display: flex;
    gap: 6px;
    margin-bottom: 10px;
    flex-wrap: wrap;
  }

  .filters button {
    font-size: 10px;
    padding: 4px 7px;
    border-radius: 6px;
    color: var(--text-muted);
    background: rgba(255, 255, 255, 0.03);
    border: 1px solid transparent;
    cursor: pointer;
  }

  .filters button.active {
    color: var(--text);
    border-color: var(--border);
    background: rgba(255, 255, 255, 0.07);
  }

  .placeholder {
    color: var(--text-muted);
    font-size: 12px;
    padding: 8px 0;
  }

  .feedback {
    margin-bottom: 10px;
    font-size: 11px;
    padding: 6px 8px;
    border-radius: 6px;
  }

  .feedback.error {
    color: #ffb3b3;
    background: rgba(255, 0, 0, 0.15);
  }

  .feedback.success {
    color: #a6ffbf;
    background: rgba(0, 128, 0, 0.15);
  }

  .message-list {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .message-item {
    padding: 8px;
    background: rgba(255, 255, 255, 0.03);
    border-radius: 6px;
    border-left: 2px solid var(--blue);
  }

  .message-item.warn {
    border-left-color: var(--yellow);
  }

  .message-item.info {
    border-left-color: var(--blue);
  }

  .message-meta {
    display: flex;
    justify-content: space-between;
    font-size: 10px;
    color: var(--text-muted);
    margin-bottom: 4px;
  }

  .message-from {
    font-weight: 600;
  }

  .message-content {
    font-size: 12px;
    color: var(--text);
    line-height: 1.4;
    white-space: pre-wrap;
    word-break: break-word;
  }

  .message-actions {
    margin-top: 8px;
    display: flex;
    gap: 6px;
  }

  .message-actions button {
    font-size: 10px;
    padding: 4px 8px;
    border-radius: 6px;
    border: 1px solid var(--border);
    background: rgba(255, 255, 255, 0.05);
    color: var(--text);
    cursor: pointer;
  }

  .message-actions button:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>

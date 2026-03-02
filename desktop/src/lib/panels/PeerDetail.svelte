<script lang="ts">
  import type { PeerInfo } from "../canvas/CanvasView.svelte";
  import { fetchMonitorPilotInbox } from "../api";

  interface Props {
    peer: PeerInfo;
  }

  let { peer }: Props = $props();

  interface InboxMessage {
    from: string;
    content: string;
    timestamp: string;
  }

  let messages = $state<InboxMessage[]>([]);
  let loading = $state(true);

  $effect(() => {
    // Load inbox when peer changes
    loading = true;
    fetchMonitorPilotInbox()
      .then((data) => {
        messages = (data.messages || []).filter(
          (m: InboxMessage) => m.from === peer.id || m.from === peer.name
        );
      })
      .catch(() => {
        messages = [];
      })
      .finally(() => {
        loading = false;
      });
  });
</script>

<div class="peer-detail">
  <div class="section">
    <div class="section-title">
      <span class="status-dot" class:online={peer.status === "online"}></span>
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
        <span class="info-value" class:online={peer.status === "online"}>
          {peer.status}
        </span>
      </div>
    </div>
  </div>

  <div class="divider"></div>

  <div class="section">
    <div class="section-title">Messages</div>
    {#if loading}
      <div class="placeholder">Loading...</div>
    {:else if messages.length === 0}
      <div class="placeholder">No messages from this peer</div>
    {:else}
      <div class="message-list">
        {#each messages as msg}
          <div class="message-item">
            <div class="message-meta">
              <span class="message-from">{msg.from}</span>
              <span class="message-time">{new Date(msg.timestamp).toLocaleTimeString()}</span>
            </div>
            <div class="message-content">{msg.content}</div>
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

  .placeholder {
    color: var(--text-muted);
    font-size: 12px;
    padding: 8px 0;
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
  }

  .message-meta {
    display: flex;
    justify-content: space-between;
    font-size: 10px;
    color: var(--text-muted);
    margin-bottom: 4px;
  }

  .message-from {
    font-weight: 500;
  }

  .message-content {
    font-size: 12px;
    color: var(--text);
    line-height: 1.4;
    white-space: pre-wrap;
  }
</style>

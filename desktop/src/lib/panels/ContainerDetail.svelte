<script lang="ts">
  import type { ContainerInfo } from "../canvas/CanvasView.svelte";

  interface Props {
    container: ContainerInfo;
  }

  let { container }: Props = $props();

  let isRunning = $derived(container.status.toLowerCase().startsWith("up"));
  let displayName = $derived(
    container.groupFolder || container.name.replace("nanoclaw-", "").split("-")[0]
  );
</script>

<div class="container-detail">
  <div class="section">
    <div class="section-title">
      <span class="status-dot" class:running={isRunning}></span>
      {displayName}
    </div>

    <div class="info-grid">
      <div class="info-row">
        <span class="info-label">Name</span>
        <span class="info-value">{container.name}</span>
      </div>
      <div class="info-row">
        <span class="info-label">Status</span>
        <span class="info-value" class:running={isRunning}>{container.status}</span>
      </div>
      <div class="info-row">
        <span class="info-label">Image</span>
        <span class="info-value">{container.image}</span>
      </div>
      <div class="info-row">
        <span class="info-label">Created</span>
        <span class="info-value">{container.created}</span>
      </div>
      {#if container.groupFolder}
        <div class="info-row">
          <span class="info-label">Group</span>
          <span class="info-value">{container.groupFolder}</span>
        </div>
      {/if}
    </div>
  </div>
</div>

<style>
  .container-detail {
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

  .status-dot.running {
    background: var(--green);
  }

  .info-grid {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .info-row {
    display: flex;
    justify-content: space-between;
    font-size: 11px;
  }

  .info-label {
    color: var(--text-muted);
    flex-shrink: 0;
  }

  .info-value {
    color: var(--text);
    text-align: right;
    word-break: break-all;
    max-width: 220px;
  }

  .info-value.running {
    color: var(--green);
  }
</style>

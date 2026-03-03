<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    onClose: () => void;
    children: Snippet;
  }

  let { onClose, children }: Props = $props();

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === "Escape") {
      onClose();
    }
  }
</script>

<svelte:window onkeydown={handleKeydown} />

<div class="detail-panel">
  <div class="panel-header">
    <button class="close-btn" onclick={onClose} title="Close">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <line x1="18" y1="6" x2="6" y2="18"/>
        <line x1="6" y1="6" x2="18" y2="18"/>
      </svg>
    </button>
  </div>
  <div class="panel-body">
    {@render children()}
  </div>
</div>

<style>
  .detail-panel {
    position: fixed;
    top: 0;
    right: 0;
    bottom: 0;
    width: var(--panel-width);
    background: var(--panel-bg);
    border-left: 1px solid rgba(var(--accent-rgb), 0.2);
    box-shadow: inset 1px 0 var(--glow-spread) rgba(var(--accent-rgb), calc(var(--glow-opacity) * 0.2));
    z-index: 30;
    display: flex;
    flex-direction: column;
    animation: slide-in 0.2s ease;
    background-image:
      linear-gradient(rgba(var(--accent-rgb), var(--grid-opacity)) 1px, transparent 1px),
      linear-gradient(90deg, rgba(var(--accent-rgb), var(--grid-opacity)) 1px, transparent 1px);
    background-size: 40px 40px;
  }

  @keyframes slide-in {
    from {
      transform: translateX(100%);
    }
    to {
      transform: translateX(0);
    }
  }

  .detail-panel::before {
    content: '';
    position: absolute;
    top: 0;
    left: 0;
    width: 16px;
    height: 16px;
    border-top: 1px solid var(--accent);
    border-left: 1px solid var(--accent);
    opacity: var(--glow-opacity);
    z-index: 1;
  }

  .detail-panel::after {
    content: '';
    position: absolute;
    bottom: 0;
    left: 0;
    width: 16px;
    height: 16px;
    border-bottom: 1px solid var(--accent);
    border-left: 1px solid var(--accent);
    opacity: var(--glow-opacity);
    z-index: 1;
  }

  .panel-header {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    padding: 12px 12px 0;
    min-height: 40px;
  }

  .close-btn {
    padding: 6px;
    border-radius: 4px;
    color: var(--text-muted);
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .close-btn:hover {
    background: rgba(255, 255, 255, 0.08);
    color: var(--text);
  }

  .panel-body {
    flex: 1;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    min-height: 0;
  }
</style>

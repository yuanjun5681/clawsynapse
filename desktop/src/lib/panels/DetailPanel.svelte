<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    children: Snippet;
  }

  let { children }: Props = $props();
</script>

<div class="detail-panel">
  <div class="panel-body">
    {@render children()}
  </div>
</div>

<style>
  .detail-panel {
    width: var(--panel-width);
    height: 100%;
    background: var(--panel-bg);
    backdrop-filter: blur(12px);
    -webkit-backdrop-filter: blur(12px);
    border-left: 1px solid rgba(var(--accent-rgb), 0.2);
    box-shadow: inset 1px 0 var(--glow-spread) rgba(var(--accent-rgb), calc(var(--glow-opacity) * 0.2));
    display: flex;
    flex-direction: column;
    background-image:
      linear-gradient(rgba(var(--accent-rgb), var(--grid-opacity)) 1px, transparent 1px),
      linear-gradient(90deg, rgba(var(--accent-rgb), var(--grid-opacity)) 1px, transparent 1px);
    background-size: 40px 40px;
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

  .panel-body {
    flex: 1;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    min-height: 0;
  }
</style>

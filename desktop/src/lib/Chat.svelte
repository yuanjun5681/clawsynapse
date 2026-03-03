<script lang="ts">
  import { tick, type Snippet } from "svelte";

  interface Props {
    userText: string;
    agentText: string;
    streaming: boolean;
    streamText: string;
    children: Snippet;
  }

  let { userText, agentText, streaming, streamText, children }: Props = $props();

  let container: HTMLDivElement;
  let userAtBottom = true;

  function checkIfAtBottom() {
    if (!container) return;
    const threshold = 30;
    userAtBottom =
      container.scrollHeight - container.scrollTop - container.clientHeight < threshold;
  }

  async function scrollToBottom() {
    await tick();
    if (container && userAtBottom) {
      container.scrollTop = container.scrollHeight;
    }
  }

  $effect(() => {
    agentText;
    streamText;
    scrollToBottom();
  });
</script>

<div class="chat" bind:this={container} onscroll={checkIfAtBottom}>
  {#if userText}
    <div class="user-text">{userText}</div>
  {/if}

  {#if streaming && streamText}
    <div class="agent-text">
      <pre>{streamText}<span class="block-cursor"></span></pre>
    </div>
  {:else if streaming}
    <div class="agent-text">
      <pre><span class="thinking">思考中...</span> <span class="block-cursor"></span></pre>
    </div>
  {:else if agentText}
    <div class="agent-text">
      <pre>{agentText}</pre>
    </div>
  {/if}

  {@render children()}
</div>

<style>
  .chat {
    flex: 1;
    overflow-y: auto;
    padding: 0 24px;
    display: flex;
    flex-direction: column;
    gap: 16px;
  }

  .user-text {
    color: var(--text);
    font-size: 13px;
    line-height: 1.5;
  }

  .agent-text {
    color: var(--text-agent);
  }

  .agent-text pre {
    text-shadow: 0 0 var(--glow-spread) rgba(var(--accent-rgb), calc(var(--glow-opacity) * 0.25));
  }

  .thinking {
    opacity: 0.9;
    animation: hud-flicker 3s linear infinite;
  }

  @keyframes hud-flicker {
    0%, 100% { opacity: 0.9; }
    92% { opacity: 0.9; }
    93% { opacity: 0.5; }
    94% { opacity: 0.9; }
    96% { opacity: 0.6; }
    97% { opacity: 0.9; }
  }

  pre {
    white-space: pre-wrap;
    font-family: inherit;
    margin: 0;
    font-size: 13px;
    line-height: 1.6;
  }

  .block-cursor {
    display: inline-block;
    width: 8px;
    height: 1.1em;
    background: var(--accent);
    vertical-align: text-bottom;
    animation: blink 1s step-end infinite;
    box-shadow: 0 0 var(--glow-spread) rgba(var(--accent-rgb), var(--glow-opacity));
  }

  @keyframes blink {
    0%, 100% { opacity: 1; }
    50% { opacity: 0; }
  }
</style>

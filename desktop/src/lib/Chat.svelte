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

  async function scrollToBottom() {
    await tick();
    if (container) {
      container.scrollTop = container.scrollHeight;
    }
  }

  $effect(() => {
    agentText;
    streamText;
    scrollToBottom();
  });
</script>

<div class="chat" bind:this={container}>
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
    color: #ddd;
    font-size: 13px;
    line-height: 1.5;
  }

  .agent-text {
    color: var(--text-agent);
  }

  .thinking {
    opacity: 0.9;
  }

  pre {
    white-space: pre-wrap;
    font-family: inherit;
    margin: 0;
    font-size: 14px;
    line-height: 1.6;
  }

  .block-cursor {
    display: inline-block;
    width: 8px;
    height: 1.1em;
    background: var(--text-agent);
    vertical-align: text-bottom;
    animation: blink 1s step-end infinite;
  }

  @keyframes blink {
    0%, 100% { opacity: 1; }
    50% { opacity: 0; }
  }
</style>

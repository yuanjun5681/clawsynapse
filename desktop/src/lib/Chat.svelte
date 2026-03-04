<script lang="ts">
  import { tick } from "svelte";
  import Avatar from "./Avatar.svelte";

  interface Message {
    role: 'user' | 'agent';
    text: string;
  }

  interface Props {
    messages: Message[];
    streaming: boolean;
    streamText: string;
    chatState: 'idle' | 'thinking' | 'streaming' | 'done';
  }

  let { messages, streaming, streamText, chatState }: Props = $props();

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
    messages;
    streamText;
    scrollToBottom();
  });
</script>

<div class="chat" bind:this={container} onscroll={checkIfAtBottom}>
  {#if messages.length === 0 && !streaming}
    <!-- Empty state -->
    <div class="empty-state">
      <div class="empty-avatar">
        <Avatar state="idle" backendStatus="running" />
      </div>
      <div class="empty-title">SYSTEM ONLINE</div>
      <div class="empty-hint">输入消息开始对话</div>
    </div>
  {:else}
    <!-- Message history -->
    {#each messages as msg, i}
      {#if i > 0}
        <div class="msg-divider"></div>
      {/if}
      {#if msg.role === 'user'}
        <div class="user-text"><span class="prompt-mark">&gt;</span> {msg.text}</div>
      {:else}
        <div class="agent-text">
          <pre>{msg.text}</pre>
        </div>
      {/if}
    {/each}

    <!-- Streaming state -->
    {#if streaming}
      <div class="msg-divider"></div>
      {#if streamText}
        <div class="agent-text">
          <pre>{streamText}<span class="block-cursor"></span></pre>
        </div>
      {:else}
        <div class="agent-text">
          <pre><span class="thinking">思考中...</span> <span class="block-cursor"></span></pre>
        </div>
      {/if}
    {/if}
  {/if}
</div>

<style>
  .chat {
    flex: 1;
    overflow-y: auto;
    padding: 0 24px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  /* Empty state */
  .empty-state {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 12px;
    padding: 32px 0;
  }

  .empty-avatar {
    width: 64px;
    height: 64px;
    opacity: 0.7;
  }

  .empty-title {
    font-family: var(--font-display);
    font-size: 14px;
    font-weight: 700;
    letter-spacing: 0.2em;
    color: var(--accent);
    text-shadow: 0 0 var(--glow-spread) rgba(var(--accent-rgb), var(--glow-opacity));
  }

  .empty-hint {
    font-size: 11px;
    color: var(--text-muted);
    opacity: 0.6;
    letter-spacing: 0.05em;
  }

  /* Messages */
  .prompt-mark {
    color: var(--accent);
    font-weight: 700;
    text-shadow: 0 0 var(--glow-spread) rgba(var(--accent-rgb), calc(var(--glow-opacity) * 0.5));
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

  .msg-divider {
    height: 1px;
    background: linear-gradient(
      90deg,
      transparent,
      rgba(var(--accent-rgb), 0.2) 20%,
      rgba(var(--accent-rgb), 0.2) 80%,
      transparent
    );
    margin: 4px 0;
  }

  /* Streaming */
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

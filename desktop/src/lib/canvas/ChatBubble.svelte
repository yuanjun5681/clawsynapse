<script lang="ts">
  import { onMount } from 'svelte';
  import type { ChatBubble } from '../state/chatBubbleStore';

  interface Props {
    x: number;
    y: number;
    bubble: ChatBubble;
  }

  let { x, y, bubble }: Props = $props();

  let isSent = $derived(bubble.direction === 'sent');
  let bubbleColor = $derived(isSent ? 'var(--accent)' : 'var(--green)');

  // We measure the actual rendered height, then shift the foreignObject up
  let wrapperEl: HTMLDivElement | undefined = $state();
  let measuredH = $state(40); // initial guess

  onMount(() => {
    if (wrapperEl) {
      measuredH = wrapperEl.offsetHeight;
    }
  });

  $effect(() => {
    // Re-measure when text changes
    bubble.text;
    if (wrapperEl) {
      // tick to let DOM update
      requestAnimationFrame(() => {
        if (wrapperEl) measuredH = wrapperEl.offsetHeight;
      });
    }
  });

  const FO_W = 300;
  const NODE_R = 32; // enough clearance above node circle
  const TAIL_H = 7;

  // foreignObject positioned so its top-left puts content above node
  let foX = $derived(x - FO_W / 2);
  let foY = $derived(y - NODE_R - TAIL_H - measuredH);
</script>

<g class="chat-bubble">
  <foreignObject x={foX} y={foY} width={FO_W} height={measuredH + TAIL_H + 2}>
    <div xmlns="http://www.w3.org/1999/xhtml" bind:this={wrapperEl} class="bubble-wrap">
      <div class="bubble-body" style:background={bubbleColor}>
        {bubble.text}
      </div>
      <svg width="10" height={TAIL_H} viewBox="0 0 10 7" class="tail">
        <polygon points="0,0 5,7 10,0" fill={bubbleColor} />
      </svg>
    </div>
  </foreignObject>
</g>

<style>
  .chat-bubble {
    pointer-events: none;
    animation: bubble-appear 0.3s ease-out forwards;
  }

  .bubble-wrap {
    display: flex;
    flex-direction: column;
    align-items: center;
  }

  .bubble-body {
    border-radius: 8px;
    padding: 8px 12px;
    max-width: 260px;
    color: #111;
    font-size: 12px;
    font-weight: 500;
    line-height: 1.45;
    word-break: break-word;
    box-shadow: 0 2px 6px rgba(0, 0, 0, 0.3);
    display: -webkit-box;
    -webkit-line-clamp: 4;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }

  .tail {
    display: block;
    flex-shrink: 0;
  }

  @keyframes bubble-appear {
    0% {
      opacity: 0;
      transform: translateY(6px);
    }
    100% {
      opacity: 1;
      transform: translateY(0);
    }
  }
</style>

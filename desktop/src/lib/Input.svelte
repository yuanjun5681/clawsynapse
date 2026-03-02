<script lang="ts">
  import { onMount } from "svelte";

  interface Props {
    disabled: boolean;
    onSend: (text: string) => void;
  }

  let { disabled, onSend }: Props = $props();
  let text = $state("");
  let textarea: HTMLTextAreaElement;
  let focused = $state(false);
  let composing = $state(false);
  let compositionJustEnded = $state(false);

  export function focus() {
    textarea?.focus();
  }

  onMount(() => {
    textarea?.focus();
  });

  function handleKeydown(e: KeyboardEvent) {
    const isImeEnter = composing || e.isComposing || compositionJustEnded || e.keyCode === 229;
    if (e.key === "Enter" && !e.shiftKey && !isImeEnter) {
      e.preventDefault();
      send();
    }
  }

  function handleCompositionEnd() {
    composing = false;
    compositionJustEnded = true;
    setTimeout(() => {
      compositionJustEnded = false;
    }, 0);
  }

  function send() {
    const trimmed = text.trim();
    if (!trimmed || disabled) return;
    onSend(trimmed);
    text = "";
    if (textarea) textarea.style.height = "auto";
  }

  function autoResize() {
    if (!textarea) return;
    textarea.style.height = "auto";
    textarea.style.height = Math.min(textarea.scrollHeight, 120) + "px";
  }
</script>

<div class="input-area">
  <div class="input-wrapper">
    <div class="input-display" aria-hidden="true">
      <span>{text}</span>{#if focused}<span class="block-cursor"></span>{/if}
    </div>
    <textarea
      bind:this={textarea}
      bind:value={text}
      oninput={autoResize}
      onkeydown={handleKeydown}
      onfocus={() => focused = true}
      onblur={() => focused = false}
      oncompositionstart={() => composing = true}
      oncompositionend={handleCompositionEnd}
      {disabled}
      rows="1"
      aria-label="Message input"
    ></textarea>
  </div>
</div>

<style>
  .input-area {
    padding: 8px 0 24px;
  }

  .input-wrapper {
    position: relative;
    min-height: 24px;
  }

  .input-display {
    padding: 8px 0;
    font-size: 14px;
    line-height: 1.5;
    color: var(--text);
    white-space: pre-wrap;
    word-break: break-word;
    pointer-events: none;
  }

  .block-cursor {
    display: inline-block;
    width: 8px;
    height: 1.1em;
    background: #ddd;
    vertical-align: text-bottom;
    animation: blink 1s step-end infinite;
  }

  @keyframes blink {
    0%, 100% { opacity: 1; }
    50% { opacity: 0; }
  }

  textarea {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    resize: none;
    border: none;
    padding: 8px 0;
    background: transparent;
    color: transparent;
    caret-color: transparent;
    font-size: 14px;
    line-height: 1.5;
    overflow: hidden;
  }

  textarea:focus {
    outline: none;
  }
</style>

<script lang="ts">
  import { onMount } from "svelte";

  interface Props {
    disabled: boolean;
    onSend: (text: string) => void;
  }

  let { disabled, onSend }: Props = $props();
  let text = $state("");
  let textarea: HTMLTextAreaElement;
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
    resetHeight();
  }

  function resetHeight() {
    if (!textarea) return;
    textarea.style.height = "auto";
  }

  function autoResize() {
    if (!textarea) return;
    textarea.style.height = "auto";
    textarea.style.height = Math.min(textarea.scrollHeight, 120) + "px";
  }
</script>

<div class="input-area">
  <textarea
    bind:this={textarea}
    bind:value={text}
    oninput={autoResize}
    onkeydown={handleKeydown}
    onfocus={() => {}}
    onblur={() => {}}
    oncompositionstart={() => composing = true}
    oncompositionend={handleCompositionEnd}
    {disabled}
    rows="1"
    placeholder={disabled ? "" : "Send a message..."}
    aria-label="Message input"
  ></textarea>
</div>

<style>
  .input-area {
    padding: 8px 0 16px;
  }

  textarea {
    display: block;
    width: 100%;
    resize: none;
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 8px 12px;
    background: var(--bg-input);
    color: var(--text);
    caret-color: var(--accent);
    font-size: 13px;
    line-height: 1.5;
    max-height: 120px;
    overflow-y: auto;
    transition: border-color 0.15s, box-shadow 0.15s;
  }

  textarea:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--ring);
  }

  textarea::placeholder {
    color: var(--text-muted);
    opacity: 0.6;
  }

  textarea:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }
</style>

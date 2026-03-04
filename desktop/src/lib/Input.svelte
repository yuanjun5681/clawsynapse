<script lang="ts">
  import { onMount } from "svelte";

  const MAX_CHARS = 500;

  interface Props {
    disabled: boolean;
    streaming: boolean;
    onSend: (text: string) => void;
  }

  let { disabled, streaming, onSend }: Props = $props();
  let text = $state("");
  let textarea: HTMLTextAreaElement;
  let composing = $state(false);
  let compositionJustEnded = $state(false);

  let charCount = $derived(text.length);
  let charColor = $derived(
    charCount >= MAX_CHARS ? "var(--red)" :
    charCount >= MAX_CHARS * 0.8 ? "var(--yellow)" :
    "var(--text-muted)"
  );

  let canSend = $derived(!disabled && text.trim().length > 0 && charCount <= MAX_CHARS);

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
    if (!trimmed || disabled || charCount > MAX_CHARS) return;
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

<div class="input-hud" class:disabled>
  <!-- HUD corner brackets -->
  <span class="corner tl"></span>
  <span class="corner tr"></span>
  <span class="corner bl"></span>
  <span class="corner br"></span>

  <div class="input-row">
    <span class="prompt">&gt;_</span>
    <textarea
      bind:this={textarea}
      bind:value={text}
      oninput={autoResize}
      onkeydown={handleKeydown}
      onfocus={() => {}}
      onblur={() => {}}
      oncompositionstart={() => composing = true}
      oncompositionend={handleCompositionEnd}
      disabled={disabled}
      rows="1"
      placeholder={disabled ? "" : "ENTER COMMAND..."}
      aria-label="Message input"
      maxlength={MAX_CHARS}
    ></textarea>
    <button
      class="send-btn"
      class:streaming
      onclick={send}
      disabled={!canSend}
      title={streaming ? "Processing..." : "Send"}
    >
      {#if streaming}
        <span class="pulse-dot"></span>
      {:else}
        <span class="send-arrow">▶</span>
      {/if}
    </button>
  </div>

  {#if text.length > 0}
    <div class="char-count" style="color: {charColor}">
      {charCount}/{MAX_CHARS}
    </div>
  {/if}
</div>

<style>
  .input-hud {
    position: relative;
    padding: 8px 12px;
    border: 1px solid var(--tron-border-color, rgba(var(--accent-rgb), 0.25));
    border-radius: var(--border-radius, 4px);
    background: var(--bg-input);
    transition: border-color 0.25s ease-out, box-shadow 0.25s ease-out;
  }

  .input-hud:focus-within {
    border-color: var(--accent);
    box-shadow:
      0 0 var(--glow-spread) rgba(var(--accent-rgb), calc(var(--glow-opacity) * 0.4)),
      inset 0 0 calc(var(--glow-spread) * 0.5) rgba(var(--accent-rgb), calc(var(--glow-opacity) * 0.1));
  }

  .input-hud.disabled {
    opacity: 0.4;
  }

  /* HUD corner brackets */
  .corner {
    position: absolute;
    width: 8px;
    height: 8px;
    pointer-events: none;
  }

  .corner.tl {
    top: -1px;
    left: -1px;
    border-top: 1px solid var(--accent);
    border-left: 1px solid var(--accent);
    opacity: var(--glow-opacity);
  }

  .corner.tr {
    top: -1px;
    right: -1px;
    border-top: 1px solid var(--accent);
    border-right: 1px solid var(--accent);
    opacity: var(--glow-opacity);
  }

  .corner.bl {
    bottom: -1px;
    left: -1px;
    border-bottom: 1px solid var(--accent);
    border-left: 1px solid var(--accent);
    opacity: var(--glow-opacity);
  }

  .corner.br {
    bottom: -1px;
    right: -1px;
    border-bottom: 1px solid var(--accent);
    border-right: 1px solid var(--accent);
    opacity: var(--glow-opacity);
  }

  .input-row {
    display: flex;
    align-items: flex-start;
    gap: 8px;
  }

  .prompt {
    font-family: var(--font-display);
    font-size: 14px;
    font-weight: 700;
    color: var(--accent);
    line-height: 1.5;
    flex-shrink: 0;
    text-shadow: 0 0 var(--glow-spread) rgba(var(--accent-rgb), var(--glow-opacity));
    user-select: none;
    padding-top: 1px;
  }

  textarea {
    display: block;
    flex: 1;
    resize: none;
    border: none;
    padding: 0;
    background: transparent;
    color: var(--text);
    caret-color: var(--accent);
    font-size: 13px;
    line-height: 1.5;
    letter-spacing: 0.05em;
    max-height: 120px;
    overflow-y: auto;
    outline: none;
  }

  textarea::placeholder {
    color: var(--text-muted);
    opacity: 0.5;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    font-size: 0.85em;
  }

  textarea:disabled {
    cursor: not-allowed;
  }

  /* Send button */
  .send-btn {
    flex-shrink: 0;
    width: 28px;
    height: 28px;
    display: flex;
    align-items: center;
    justify-content: center;
    border: 1px solid rgba(var(--accent-rgb), 0.4);
    border-radius: var(--border-radius, 4px);
    background: transparent;
    color: var(--accent);
    cursor: pointer;
    transition: all 0.2s ease-out;
    padding: 0;
  }

  .send-btn:hover:not(:disabled) {
    background: rgba(var(--accent-rgb), 0.1);
    border-color: var(--accent);
    box-shadow: 0 0 var(--glow-spread) rgba(var(--accent-rgb), calc(var(--glow-opacity) * 0.5));
  }

  .send-btn:disabled {
    opacity: 0.3;
    cursor: not-allowed;
  }

  .send-arrow {
    font-size: 11px;
    line-height: 1;
  }

  .pulse-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--accent);
    animation: pulse 1.2s ease-in-out infinite;
  }

  @keyframes pulse {
    0%, 100% { opacity: 0.4; transform: scale(0.8); }
    50% { opacity: 1; transform: scale(1.1); }
  }

  .char-count {
    text-align: right;
    font-size: 10px;
    margin-top: 4px;
    letter-spacing: 0.05em;
    opacity: 0.7;
    font-family: var(--font-display);
  }
</style>

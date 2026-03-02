<script lang="ts">
  import { invoke } from "@tauri-apps/api/core";
  import { onMount } from "svelte";

  interface Props {
    onClose: () => void;
  }

  let { onClose }: Props = $props();

  let saving = $state(false);
  let loading = $state(true);

  let agnoKey = $state("");
  let originalAgnoKey = $state("");
  let agnoModelId = $state("");
  let agnoBaseUrl = $state("");

  // Advanced
  let temperature = $state("");
  let maxTokens = $state("");

  function maskKey(key: string): string {
    if (!key || key.length <= 4) return key;
    return "\u2022".repeat(8) + key.slice(-4);
  }

  function resolveKey(current: string, original: string): string {
    // If the user didn't touch the masked value, keep the original
    if (current === maskKey(original)) return original;
    return current;
  }

  let canSave = $derived(
    agnoKey.trim().length > 0 &&
    agnoModelId.trim().length > 0 &&
    agnoBaseUrl.trim().length > 0
  );

  onMount(async () => {
    try {
      const pairs: [string, string][] = await invoke("read_env_config");
      const env = Object.fromEntries(pairs);

      if (env.AGNO_API_KEY) {
        originalAgnoKey = env.AGNO_API_KEY;
        agnoKey = maskKey(env.AGNO_API_KEY);
      }
      if (env.AGNO_MODEL_ID) agnoModelId = env.AGNO_MODEL_ID;
      if (env.AGNO_BASE_URL) agnoBaseUrl = env.AGNO_BASE_URL;
      if (env.TEMPERATURE) temperature = env.TEMPERATURE;
      if (env.MAX_TOKENS) maxTokens = env.MAX_TOKENS;
    } catch (e) {
      console.error("Failed to load config:", e);
    }
    loading = false;
  });

  async function handleSave() {
    if (!canSave || saving) return;
    saving = true;

    try {
      const entries: [string, string][] = [
        ["AGNO_API_KEY", resolveKey(agnoKey.trim(), originalAgnoKey)],
        ["AGNO_MODEL_ID", agnoModelId.trim()],
        ["AGNO_BASE_URL", agnoBaseUrl.trim()],
      ];

      if (temperature.trim()) {
        entries.push(["TEMPERATURE", temperature.trim()]);
      } else {
        entries.push(["TEMPERATURE", ""]);
      }
      if (maxTokens.trim()) {
        entries.push(["MAX_TOKENS", maxTokens.trim()]);
      } else {
        entries.push(["MAX_TOKENS", ""]);
      }

      await invoke("save_env_config", { entries });
      await invoke("restart_backend");
      onClose();
    } catch (e) {
      console.error("Failed to save config:", e);
      saving = false;
    }
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === "Enter") handleSave();
  }
</script>

<div class="settings">
  <div class="settings-card">
    <h1>Settings</h1>

    {#if loading}
      <p class="muted">Loading configuration...</p>
    {:else}
      <div class="key-form">
        <label class="field">
          <span class="field-label">API Key</span>
          <input
            type="password"
            bind:value={agnoKey}
            placeholder="AGNO_API_KEY"
            onkeydown={handleKeydown}
            aria-label="API key"
          />
        </label>
        <label class="field">
          <span class="field-label">Model ID</span>
          <input
            type="text"
            bind:value={agnoModelId}
            placeholder="AGNO_MODEL_ID"
            onkeydown={handleKeydown}
            aria-label="Model ID"
          />
        </label>
        <label class="field">
          <span class="field-label">Base URL</span>
          <input
            type="url"
            bind:value={agnoBaseUrl}
            placeholder="AGNO_BASE_URL"
            onkeydown={handleKeydown}
            aria-label="Base URL"
          />
        </label>

        <details class="advanced">
          <summary>Advanced</summary>
          <div class="advanced-fields">
            <label class="field">
              <span class="field-label">Temperature</span>
              <input
                type="text"
                bind:value={temperature}
                placeholder="0.7"
                onkeydown={handleKeydown}
                aria-label="Temperature"
              />
            </label>
            <label class="field">
              <span class="field-label">Max Tokens</span>
              <input
                type="text"
                bind:value={maxTokens}
                placeholder="8192"
                onkeydown={handleKeydown}
                aria-label="Max tokens"
              />
            </label>
          </div>
        </details>
      </div>

      <div class="actions">
        <button class="cancel-btn" onclick={onClose} disabled={saving}>
          Cancel
        </button>
        <button
          class="save-btn"
          onclick={handleSave}
          disabled={saving || !canSave}
        >
          {saving ? "Saving..." : "Save & Restart"}
        </button>
      </div>
    {/if}
  </div>
</div>

<style>
  .settings {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 100%;
    padding: 2rem;
  }

  .settings-card {
    max-width: 480px;
    width: 100%;
  }

  h1 {
    font-size: 1.4rem;
    font-weight: 600;
    margin-bottom: 1.5rem;
  }

  .muted {
    color: var(--text-muted);
    font-size: 0.85rem;
  }

  .key-form {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    align-items: stretch;
    margin-bottom: 2rem;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    min-width: 0;
  }

  .field-label {
    color: var(--text-muted);
    font-size: 0.78rem;
  }

  .key-form select {
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 0.45rem 0.55rem;
    color: var(--text);
    font-size: 0.85rem;
  }

  .key-form input {
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 0.45rem 0.55rem;
    width: 100%;
    font-size: 0.85rem;
  }

  .key-form select:focus,
  .key-form input:focus {
    outline: 1px solid color-mix(in srgb, var(--accent) 75%, #fff 25%);
    outline-offset: 0;
  }

  .advanced {
    margin-top: 0.5rem;
  }

  .advanced summary {
    color: var(--text-muted);
    font-size: 0.85rem;
    cursor: pointer;
    user-select: none;
  }

  .advanced-fields {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    margin-top: 0.6rem;
  }

  .actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.75rem;
  }

  .cancel-btn {
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.5rem 1rem;
    font-size: 0.9rem;
    color: var(--text);
    cursor: pointer;
  }

  .cancel-btn:hover:not(:disabled) {
    background: var(--border);
  }

  .save-btn {
    background: var(--accent);
    border-radius: 6px;
    padding: 0.5rem 1.25rem;
    font-size: 0.9rem;
    color: #fff;
    font-weight: 500;
    cursor: pointer;
  }

  .save-btn:hover:not(:disabled) {
    filter: brightness(1.1);
  }

  .save-btn:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }
</style>

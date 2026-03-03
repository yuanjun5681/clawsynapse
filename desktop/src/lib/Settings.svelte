<script lang="ts">
  import { invoke } from "@tauri-apps/api/core";
  import { onMount } from "svelte";
  import {
    THEMES, THEME_NAMES, INTENSITIES,
    getTheme, getIntensity, setTheme, setIntensity,
    type ThemeName, type Intensity
  } from "./theme.svelte";

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

  // Appearance
  let currentTheme = $state<ThemeName>(getTheme());
  let currentIntensity = $state<Intensity>(getIntensity());

  function pickTheme(t: ThemeName) {
    currentTheme = t;
    setTheme(t);
  }

  function pickIntensity(i: Intensity) {
    currentIntensity = i;
    setIntensity(i);
  }

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

      <!-- Appearance -->
      <div class="appearance-section">
        <h2>Appearance</h2>
        <div class="appearance-row">
          <span class="appearance-label">THEME</span>
          <div class="theme-dots">
            {#each THEME_NAMES as t}
              <button
                class="theme-dot"
                class:active={currentTheme === t}
                style="--dot-color: {THEMES[t].color}"
                onclick={() => pickTheme(t)}
                title={THEMES[t].label}
              >
                <span class="dot-inner"></span>
              </button>
            {/each}
          </div>
        </div>
        <div class="theme-labels">
          {#each THEME_NAMES as t}
            <span class="theme-label" class:active={currentTheme === t}>{THEMES[t].label}</span>
          {/each}
        </div>
        <div class="appearance-row">
          <span class="appearance-label">GLOW</span>
          <div class="intensity-btns">
            {#each INTENSITIES as i}
              <button
                class="intensity-btn"
                class:active={currentIntensity === i}
                onclick={() => pickIntensity(i)}
              >
                {i.charAt(0).toUpperCase() + i.slice(1)}
              </button>
            {/each}
          </div>
        </div>
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

  /* --- Appearance --- */
  .appearance-section {
    margin-bottom: 2rem;
    padding-top: 1.5rem;
    border-top: 1px solid var(--border);
  }

  .appearance-section h2 {
    font-size: 1rem;
    font-weight: 600;
    margin-bottom: 1rem;
    color: var(--text);
    letter-spacing: 1px;
    text-transform: uppercase;
  }

  .appearance-row {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 6px;
  }

  .appearance-label {
    font-size: 0.75rem;
    color: var(--text-muted);
    letter-spacing: 1px;
    width: 52px;
    flex-shrink: 0;
  }

  .theme-dots {
    display: flex;
    gap: 8px;
  }

  .theme-dot {
    width: 24px;
    height: 24px;
    border-radius: 50%;
    border: 2px solid transparent;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0;
    transition: border-color 0.15s, box-shadow 0.15s;
  }

  .theme-dot .dot-inner {
    width: 14px;
    height: 14px;
    border-radius: 50%;
    background: var(--dot-color);
  }

  .theme-dot:hover {
    border-color: var(--dot-color);
  }

  .theme-dot.active {
    border-color: var(--dot-color);
    box-shadow: 0 0 8px var(--dot-color);
  }

  .theme-labels {
    display: flex;
    gap: 8px;
    margin-left: 64px;
    margin-bottom: 12px;
  }

  .theme-label {
    width: 24px;
    text-align: center;
    font-size: 7px;
    color: var(--text-muted);
    letter-spacing: 0.5px;
    opacity: 0.5;
  }

  .theme-label.active {
    color: var(--accent);
    opacity: 1;
  }

  .intensity-btns {
    display: flex;
    gap: 4px;
  }

  .intensity-btn {
    padding: 4px 10px;
    font-size: 0.75rem;
    border: 1px solid var(--border);
    border-radius: 4px;
    background: transparent;
    color: var(--text-muted);
    transition: all 0.15s;
    letter-spacing: 0.5px;
  }

  .intensity-btn:hover {
    border-color: var(--accent);
    color: var(--text);
  }

  .intensity-btn.active {
    border-color: var(--accent);
    color: var(--accent);
    box-shadow: 0 0 var(--glow-spread) rgba(var(--accent-rgb), var(--glow-opacity));
  }
</style>

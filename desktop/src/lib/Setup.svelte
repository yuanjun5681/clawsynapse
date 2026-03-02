<script lang="ts">
  import { invoke } from "@tauri-apps/api/core";

  interface SetupStatus {
    nodeInstalled: boolean;
    nodeVersion: string;
    dockerRunning: boolean;
    containerImageBuilt: boolean;
    containerResourcesReady: boolean;
    apiKeyConfigured: boolean;
    userDataDir: string;
  }

  interface Props {
    onComplete: () => void;
  }

  let { onComplete }: Props = $props();

  let status = $state<SetupStatus | null>(null);
  let loading = $state(true);
  let buildingImage = $state(false);
  let buildOutput = $state("");
  let savingConfig = $state(false);
  let saveMessage = $state("");

  let provider = $state<"anthropic" | "agno">("agno");

  // Anthropic fields
  let anthropicKey = $state("");

  // Agno fields
  let agnoKey = $state("");
  let agnoModelId = $state("");
  let agnoBaseUrl = $state("");

  let allPassed = $derived(
    status !== null &&
      status.nodeInstalled &&
      status.dockerRunning &&
      status.containerResourcesReady &&
      status.containerImageBuilt &&
      status.apiKeyConfigured
  );

  async function refresh() {
    loading = true;
    try {
      status = await invoke<SetupStatus>("check_setup");
    } catch (e) {
      console.error("check_setup failed:", e);
    }
    loading = false;
  }

  async function handleBuildImage() {
    buildingImage = true;
    buildOutput = "";
    try {
      const result = await invoke<string>("build_container_image");
      buildOutput = result;
      await refresh();
    } catch (e: unknown) {
      buildOutput = e instanceof Error ? e.message : String(e);
    }
    buildingImage = false;
  }

  let canSave = $derived(
    provider === "anthropic"
      ? anthropicKey.trim().length > 0
      : agnoKey.trim().length > 0 &&
          agnoModelId.trim().length > 0 &&
          agnoBaseUrl.trim().length > 0
  );

  async function handleSaveConfig() {
    if (!canSave) return;
    savingConfig = true;
    saveMessage = "";
    try {
      const entries =
        provider === "anthropic"
          ? [["ANTHROPIC_API_KEY", anthropicKey.trim()]]
          : [
              ["AGNO_API_KEY", agnoKey.trim()],
              ["AGNO_MODEL_ID", agnoModelId.trim()],
              ["AGNO_BASE_URL", agnoBaseUrl.trim()],
            ];

      await invoke("save_env_config", {
        entries,
      });
      saveMessage = "Saved to .env";
      if (provider === "anthropic") {
        anthropicKey = "";
      } else {
        agnoKey = "";
        agnoModelId = "";
        agnoBaseUrl = "";
      }
      await refresh();
    } catch (e: unknown) {
      saveMessage = e instanceof Error ? e.message : String(e);
    }
    savingConfig = false;
  }

  // Initial check
  refresh();
</script>

<div class="setup">
  <div class="setup-card">
    <h1>NanoClaw Setup</h1>

    {#if loading && !status}
      <p class="muted">Checking prerequisites...</p>
    {:else if status}
      <div class="checks">
        <!-- Node.js -->
        <div class="check-row">
          <span class="icon" class:pass={status.nodeInstalled} class:fail={!status.nodeInstalled}>
            {status.nodeInstalled ? "✓" : "✗"}
          </span>
          <div class="check-info">
            <span class="check-label">Node.js</span>
            {#if status.nodeInstalled}
              <span class="muted">{status.nodeVersion}</span>
            {:else}
              <span class="hint">Install from <a href="https://nodejs.org" target="_blank" rel="noopener">nodejs.org</a></span>
            {/if}
          </div>
        </div>

        <!-- Docker -->
        <div class="check-row">
          <span class="icon" class:pass={status.dockerRunning} class:fail={!status.dockerRunning}>
            {status.dockerRunning ? "✓" : "✗"}
          </span>
          <div class="check-info">
            <span class="check-label">Docker</span>
            {#if status.dockerRunning}
              <span class="muted">Running</span>
            {:else}
              <span class="hint">Start Docker Desktop</span>
            {/if}
          </div>
        </div>

        <!-- Container Image -->
        <div class="check-row">
          <span class="icon"
            class:pass={status.containerImageBuilt}
            class:pending={!status.containerImageBuilt}>
            {status.containerImageBuilt ? "✓" : "○"}
          </span>
          <div class="check-info">
            <span class="check-label">Container Image</span>
            {#if status.containerImageBuilt}
              <span class="muted">Built</span>
            {:else}
              <button
                class="action-btn"
                onclick={handleBuildImage}
                disabled={buildingImage || !status.dockerRunning}
              >
                {buildingImage ? "Building..." : "Build Image"}
              </button>
            {/if}
            {#if buildOutput}
              <pre class="build-output">{buildOutput}</pre>
            {/if}
          </div>
        </div>

        <!-- Bundled Resources -->
        <div class="check-row">
          <span class="icon"
            class:pass={status.containerResourcesReady}
            class:fail={!status.containerResourcesReady}>
            {status.containerResourcesReady ? "✓" : "✗"}
          </span>
          <div class="check-info">
            <span class="check-label">Bundled Resources</span>
            {#if status.containerResourcesReady}
              <span class="muted">container-agno available</span>
            {:else}
              <span class="hint">Missing container-agno resource in app bundle.</span>
            {/if}
          </div>
        </div>

        <!-- API / Model Config -->
        <div class="check-row">
          <span class="icon"
            class:pass={status.apiKeyConfigured}
            class:pending={!status.apiKeyConfigured}>
            {status.apiKeyConfigured ? "✓" : "○"}
          </span>
          <div class="check-info">
            <span class="check-label">API / Model Config</span>
            {#if status.apiKeyConfigured}
              <span class="muted">Configured</span>
            {:else}
              <div class="key-form">
                <label class="field">
                  <span class="field-label">Provider</span>
                  <select bind:value={provider} aria-label="Model provider">
                    <option value="anthropic">Anthropic</option>
                    <option value="agno">Agno / Custom</option>
                  </select>
                </label>
                {#if provider === "anthropic"}
                  <label class="field">
                    <span class="field-label">API Key</span>
                    <input
                      type="password"
                      bind:value={anthropicKey}
                      placeholder="ANTHROPIC_API_KEY"
                      onkeydown={(e) => e.key === "Enter" && handleSaveConfig()}
                      aria-label="Anthropic API key"
                    />
                  </label>
                {:else}
                  <label class="field">
                    <span class="field-label">API Key</span>
                    <input
                      type="password"
                      bind:value={agnoKey}
                      placeholder="AGNO_API_KEY"
                      onkeydown={(e) => e.key === "Enter" && handleSaveConfig()}
                      aria-label="Agno API key"
                    />
                  </label>
                  <label class="field">
                    <span class="field-label">Model ID</span>
                    <input
                      type="text"
                      bind:value={agnoModelId}
                      placeholder="AGNO_MODEL_ID"
                      onkeydown={(e) => e.key === "Enter" && handleSaveConfig()}
                      aria-label="Agno model ID"
                    />
                  </label>
                  <label class="field">
                    <span class="field-label">Base URL</span>
                    <input
                      type="url"
                      bind:value={agnoBaseUrl}
                      placeholder="AGNO_BASE_URL"
                      onkeydown={(e) => e.key === "Enter" && handleSaveConfig()}
                      aria-label="Agno base URL"
                    />
                  </label>
                {/if}
                <button
                  class="action-btn save-btn"
                  onclick={handleSaveConfig}
                  disabled={savingConfig || !canSave}
                >
                  {savingConfig ? "Saving..." : "Save"}
                </button>
              </div>
              <span class="hint">Agno requires AGNO_API_KEY, AGNO_MODEL_ID, and AGNO_BASE_URL.</span>
              {#if saveMessage}
                <span class="muted">{saveMessage}</span>
              {/if}
            {/if}
          </div>
        </div>
      </div>

      <div class="actions">
        <button class="refresh-btn" onclick={refresh} disabled={loading}>
          {loading ? "Checking..." : "Refresh"}
        </button>
        <button
          class="continue-btn"
          onclick={onComplete}
          disabled={!allPassed}
        >
          Continue
        </button>
      </div>
    {/if}
  </div>
</div>

<style>
  .setup {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 100%;
    padding: 2rem;
  }

  .setup-card {
    max-width: 480px;
    width: 100%;
  }

  h1 {
    font-size: 1.4rem;
    font-weight: 600;
    margin-bottom: 1.5rem;
  }

  .checks {
    display: flex;
    flex-direction: column;
    gap: 1.25rem;
    margin-bottom: 2rem;
  }

  .check-row {
    display: flex;
    align-items: flex-start;
    gap: 0.75rem;
  }

  .icon {
    font-size: 1.1rem;
    font-weight: 700;
    width: 1.5rem;
    text-align: center;
    flex-shrink: 0;
    margin-top: 1px;
  }

  .icon.pass {
    color: var(--green);
  }

  .icon.fail {
    color: var(--red);
  }

  .icon.pending {
    color: var(--text-muted);
  }

  .check-info {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    min-width: 0;
    flex: 1;
  }

  .check-label {
    font-weight: 500;
  }

  .muted {
    color: var(--text-muted);
    font-size: 0.85rem;
  }

  .hint {
    color: var(--text-muted);
    font-size: 0.85rem;
  }

  .hint a {
    color: var(--accent);
    text-decoration: none;
  }

  .hint a:hover {
    text-decoration: underline;
  }

  .key-form {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    align-items: stretch;
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

  .action-btn {
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 0.35rem 0.75rem;
    font-size: 0.85rem;
    color: var(--text);
    cursor: pointer;
    white-space: nowrap;
  }

  .save-btn {
    align-self: flex-start;
    margin-top: 0.15rem;
  }

  .action-btn:hover:not(:disabled) {
    background: var(--border);
  }

  .build-output {
    background: var(--bg-input);
    border-radius: 4px;
    padding: 0.5rem;
    font-size: 0.75rem;
    max-height: 120px;
    overflow-y: auto;
    white-space: pre-wrap;
    word-break: break-all;
    color: var(--text-muted);
    margin-top: 0.25rem;
  }

  .actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.75rem;
  }

  .refresh-btn {
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.5rem 1rem;
    font-size: 0.9rem;
    color: var(--text);
    cursor: pointer;
  }

  .refresh-btn:hover:not(:disabled) {
    background: var(--border);
  }

  .continue-btn {
    background: var(--accent);
    border-radius: 6px;
    padding: 0.5rem 1.25rem;
    font-size: 0.9rem;
    color: #fff;
    font-weight: 500;
    cursor: pointer;
  }

  .continue-btn:hover:not(:disabled) {
    filter: brightness(1.1);
  }

  .continue-btn:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }
</style>

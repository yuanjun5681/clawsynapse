<script lang="ts">
  import { listen } from "@tauri-apps/api/event";
  import { invoke } from "@tauri-apps/api/core";
  import { getVersion } from "@tauri-apps/api/app";
  import { getCurrentWindow } from "@tauri-apps/api/window";
  import { onMount, tick } from "svelte";

  import Chat from "./lib/Chat.svelte";
  import Input from "./lib/Input.svelte";
  import Setup from "./lib/Setup.svelte";
  import Settings from "./lib/Settings.svelte";
  import Avatar from "./lib/Avatar.svelte";
  import { streamChat, checkHealth, configureApi } from "./lib/api";

  interface SetupStatus {
    nodeInstalled: boolean;
    nodeVersion: string;
    dockerRunning: boolean;
    containerImageBuilt: boolean;
    containerResourcesReady: boolean;
    apiKeyConfigured: boolean;
    userDataDir: string;
  }

  const groupId = "main";

  let setupComplete = $state(false);
  let checkingSetup = $state(true);
  let showSettings = $state(false);

  let backendReady = $state(false);
  let backendStarting = $state(true);
  let streaming = $state(false);
  let streamText = $state("");
  let userText = $state("");
  let agentText = $state("");
  let inputRef = $state<ReturnType<typeof Input> | undefined>(undefined);
  let failedHealthChecks = 0;

  let status = $derived<"running" | "starting" | "stopped">(
    backendReady ? "running" : backendStarting ? "starting" : "stopped"
  );

  let justFinished = $state(false);
  let finishedTimer: ReturnType<typeof setTimeout> | null = null;

  let chatState = $derived<'idle' | 'thinking' | 'streaming' | 'done'>(
    justFinished ? 'done' :
    streaming && streamText ? 'streaming' :
    streaming ? 'thinking' :
    'idle'
  );

  let appVersion = $state("");

  let disposed = false;
  let healthCheck: ReturnType<typeof setInterval> | null = null;
  let unlistenReady: (() => void) | null = null;
  let unlistenStopped: (() => void) | null = null;

  async function probeHealth() {
    const healthy = await checkHealth();
    if (healthy) {
      backendReady = true;
      backendStarting = false;
      failedHealthChecks = 0;
      return;
    }

    backendReady = false;
    failedHealthChecks += 1;
    if (backendStarting && failedHealthChecks >= 3) {
      backendStarting = false;
    }
  }

  function allChecksPass(s: SetupStatus): boolean {
    return (
      s.nodeInstalled &&
      s.dockerRunning &&
      s.containerResourcesReady &&
      s.containerImageBuilt &&
      s.apiKeyConfigured
    );
  }

  async function init() {
    backendStarting = true;
    failedHealthChecks = 0;

    try {
      const config = await invoke<{ baseUrl: string; authToken: string | null }>(
        "get_backend_config"
      );
      configureApi(config);
    } catch {
      // fallback to default API base in api.ts
    }

    try {
      unlistenReady = await listen("backend-ready", () => {
        backendReady = true;
        backendStarting = false;
        failedHealthChecks = 0;
      });

      unlistenStopped = await listen("backend-stopped", () => {
        backendReady = false;
        backendStarting = false;
      });

      if (disposed) {
        unlistenReady();
        unlistenStopped();
        return;
      }
    } catch {
      // event bridge unavailable; health polling still covers readiness
    }

    try {
      backendReady = await invoke<boolean>("get_backend_status");
      if (backendReady) {
        backendStarting = false;
        failedHealthChecks = 0;
      }
    } catch {
      backendReady = false;
    }

    if (!backendReady) {
      await probeHealth();
    }

    healthCheck = setInterval(async () => {
      await probeHealth();
    }, 2000);
  }

  function handleSetupComplete() {
    setupComplete = true;
    init();
  }

  onMount(() => {
    getVersion().then((v) => { appVersion = v; }).catch(() => {});

    invoke<SetupStatus>("check_setup").then((s) => {
      setupComplete = allChecksPass(s);
      checkingSetup = false;
      if (setupComplete) {
        init();
      }
    }).catch(() => {
      setupComplete = true;
      checkingSetup = false;
      init();
    });

    return () => {
      disposed = true;
      if (healthCheck) {
        clearInterval(healthCheck);
      }
      if (finishedTimer) {
        clearTimeout(finishedTimer);
      }
      if (unlistenReady) {
        unlistenReady();
      }
      if (unlistenStopped) {
        unlistenStopped();
      }
    };
  });

  async function handleSend(text: string) {
    if (streaming || !backendReady) return;

    // Clear previous round, start new one
    userText = text;
    agentText = "";
    streaming = true;
    streamText = "";

    try {
      for await (const event of streamChat(text, groupId)) {
        if (event.type === "message" && event.data.text) {
          streamText += event.data.text;
        } else if (event.type === "error") {
          const errorText = event.data.error || "An error occurred";
          if (streamText) {
            agentText = streamText + `\n\nError: ${errorText}`;
          } else {
            agentText = `Error: ${errorText}`;
          }
          streamText = "";
          break;
        } else if (event.type === "done") {
          break;
        }
      }
    } catch (e: unknown) {
      const errorText = e instanceof Error ? e.message : "Connection failed";
      agentText = `Error: ${errorText}`;
      const healthy = await checkHealth();
      backendReady = healthy;
      backendStarting = false;
    }

    // Commit streamed text as complete
    if (streamText) {
      agentText = streamText;
    }

    streaming = false;
    streamText = "";

    // Trigger "done" animation for 1.5s
    justFinished = true;
    if (finishedTimer) clearTimeout(finishedTimer);
    finishedTimer = setTimeout(() => { justFinished = false; }, 1500);

    await tick();
    inputRef?.focus();
    requestAnimationFrame(() => {
      inputRef?.focus();
    });
  }

  async function restartBackend() {
    try {
      backendReady = false;
      backendStarting = true;
      failedHealthChecks = 0;
      await invoke("restart_backend");
    } catch (e: unknown) {
      console.error("Failed to restart:", e);
      backendStarting = false;
    }
  }

  function handleDrag(e: MouseEvent) {
    if (e.button === 0 && e.detail === 1) {
      getCurrentWindow().startDragging();
    }
  }

  function handleDragDblClick() {
    getCurrentWindow().toggleMaximize();
  }
</script>

{#if checkingSetup}
  <div class="loading">
    <p>Loading...</p>
  </div>
{:else if !setupComplete}
  <Setup onComplete={handleSetupComplete} />
{:else if showSettings}
  <Settings onClose={() => { showSettings = false; backendStarting = true; failedHealthChecks = 0; }} />
{:else}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div class="app" onclick={() => inputRef?.focus()}>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <div class="drag-region" onmousedown={handleDrag} ondblclick={handleDragDblClick} onclick={() => inputRef?.focus()}></div>

    <div class="header">
      <button class="logo-btn" onclick={restartBackend} title="Restart backend">
        <Avatar state={chatState} backendStatus={status} />
      </button>
      <button class="gear-btn" onclick={() => { showSettings = true; }} title="Settings">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/>
          <circle cx="12" cy="12" r="3"/>
        </svg>
      </button>
      {#if appVersion}<span class="version">v{appVersion}</span>{/if}
    </div>

    <div class="content">
      <Chat {userText} {agentText} {streaming} {streamText}>
        <Input bind:this={inputRef} disabled={!backendReady || streaming} onSend={handleSend} />
      </Chat>
    </div>
  </div>
{/if}

<style>
  .loading {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 100%;
    color: var(--text-muted);
  }

  .app {
    display: flex;
    flex-direction: column;
    height: 100%;
  }

  .drag-region {
    height: 40px;
    min-height: 40px;
    -webkit-app-region: drag;
  }

  .header {
    padding: 8px 24px 12px;
    display: flex;
    align-items: center;
  }

  .logo-btn {
    position: relative;
    width: 64px;
    height: 64px;
    padding: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    border-radius: 50%;
  }

  .gear-btn {
    margin-left: auto;
    padding: 6px;
    border-radius: 6px;
    color: var(--text-muted);
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .gear-btn:hover {
    background: rgba(255, 255, 255, 0.06);
    color: var(--text);
  }

  .version {
    margin-left: 8px;
    font-size: 11px;
    color: var(--text-muted);
    opacity: 0.8;
    user-select: none;
  }

  .logo-btn:hover {
    background: rgba(255, 255, 255, 0.06);
  }

  .content {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 0;
  }
</style>

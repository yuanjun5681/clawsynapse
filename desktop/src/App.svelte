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
  import CanvasView from "./lib/canvas/CanvasView.svelte";
  import type {
    MonitorStatus,
    ContainerInfo,
    TaskInfo,
    PilotInfo,
    PeerInfo,
  } from "./lib/canvas/CanvasView.svelte";
  import DetailPanel from "./lib/panels/DetailPanel.svelte";
  import LocalDetail from "./lib/panels/LocalDetail.svelte";
  import PeerDetail from "./lib/panels/PeerDetail.svelte";
  import ContainerDetail from "./lib/panels/ContainerDetail.svelte";
  import {
    streamChat,
    checkHealth,
    configureApi,
    fetchMonitorStatus,
    fetchMonitorContainers,
    fetchMonitorTasks,
    fetchMonitorPilot,
  } from "./lib/api";
  import { pilotEventStore } from "./lib/state/pilotEventStore";
  import { pilotGraphStore } from "./lib/state/pilotGraphStore";
  import { monitorStreamStore, agentIpcMessages } from "./lib/state/monitorStreamStore";
  import { canonicalizeNodeId } from "./lib/domain/pilot/pilot-events";
  // Initialize theme system (side effect: sets data-theme/data-intensity on <html>)
  import "./lib/theme.svelte";

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
  interface Message {
    role: 'user' | 'agent';
    text: string;
  }

  let streaming = $state(false);
  let streamText = $state("");
  let messages = $state<Message[]>([]);
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
  let isMac = $state(navigator.platform.toUpperCase().includes("MAC"));

  let disposed = false;
  let healthCheck: ReturnType<typeof setInterval> | null = null;
  let unlistenReady: (() => void) | null = null;
  let unlistenStopped: (() => void) | null = null;
  let unsubIpcMessages: (() => void) | null = null;
  let lastIpcCount = 0;

  // --- Monitor state ---
  let monitorStatus = $state<MonitorStatus | null>(null);
  let monitorContainers = $state<ContainerInfo[]>([]);
  let monitorTasks = $state<TaskInfo[]>([]);
  let monitorPilot = $state<PilotInfo | null>(null);

  let monitorTimers: ReturnType<typeof setInterval>[] = [];
  let monitorPollingActive = $state(false);

  // --- Panel state ---
  type PanelView =
    | { type: "local" }
    | { type: "peer"; peer: PeerInfo }
    | { type: "container"; container: ContainerInfo };

  let panelView = $state<PanelView>({ type: "local" });
  function openLocalPanel() {
    pilotGraphStore.setSelectedNode("local");
    panelView = { type: "local" };
  }

  function openPeerPanel(peer: PeerInfo) {
    const peerNodeId = canonicalizeNodeId(peer.id);
    pilotGraphStore.setSelectedNode(peerNodeId);
    pilotEventStore.markNodeRead(peerNodeId);
    panelView = { type: "peer", peer };
  }

  function openContainerPanel(container: ContainerInfo) {
    pilotGraphStore.setSelectedNode(container.name);
    panelView = { type: "container", container };
  }

  function resetToLocal() {
    pilotGraphStore.setSelectedNode("local");
    panelView = { type: "local" };
  }

  // --- Health check ---
  async function probeHealth() {
    const healthy = await checkHealth();
    if (healthy) {
      backendReady = true;
      backendStarting = false;
      failedHealthChecks = 0;
      return;
    }

    failedHealthChecks += 1;
    // Only mark as down after 3 consecutive failures to avoid flicker
    if (failedHealthChecks >= 3) {
      backendReady = false;
      if (backendStarting) {
        backendStarting = false;
      }
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

  // --- Monitor polling ---
  function startMonitorPolling() {
    if (monitorPollingActive) return;
    monitorPollingActive = true;
    monitorStreamStore.start();

    // Status: every 3s
    const pollStatus = async () => {
      if (!backendReady) return;
      try {
        monitorStatus = await fetchMonitorStatus();
      } catch { /* ignore */ }
    };
    pollStatus();
    monitorTimers.push(setInterval(pollStatus, 3000));

    // Containers: every 5s
    const pollContainers = async () => {
      if (!backendReady) return;
      try {
        monitorContainers = await fetchMonitorContainers();
      } catch { /* ignore */ }
    };
    pollContainers();
    monitorTimers.push(setInterval(pollContainers, 5000));

    // Tasks: every 10s
    const pollTasks = async () => {
      if (!backendReady) return;
      try {
        const result = await fetchMonitorTasks();
        monitorTasks = result.tasks;
      } catch { /* ignore */ }
    };
    pollTasks();
    monitorTimers.push(setInterval(pollTasks, 10000));

    // Pilot: every 15s
    const pollPilot = async () => {
      if (!backendReady) return;
      try {
        monitorPilot = await fetchMonitorPilot();
        const trustedIds = monitorPilot.trustedPeers.map((peer) => canonicalizeNodeId(peer.id));
        const pendingIds = monitorPilot.pendingHandshakes.map((peer) => canonicalizeNodeId(peer.id));
        pilotGraphStore.syncKnownNodes([...trustedIds, ...pendingIds]);
      } catch { /* ignore */ }
    };
    pollPilot();
    monitorTimers.push(setInterval(pollPilot, 15000));
  }

  function stopMonitorPolling() {
    if (!monitorPollingActive) return;
    monitorPollingActive = false;

    for (const t of monitorTimers) clearInterval(t);
    monitorTimers = [];
    monitorStreamStore.stop();
  }

  // --- Init ---
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
        startMonitorPolling();
      });

      unlistenStopped = await listen("backend-stopped", () => {
        backendReady = false;
        backendStarting = false;
        stopMonitorPolling();
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
        startMonitorPolling();
      }
    } catch {
      backendReady = false;
    }

    if (!backendReady) {
      await probeHealth();
      if (backendReady) {
        startMonitorPolling();
      }
    }

    healthCheck = setInterval(async () => {
      const wasBefore = backendReady;
      await probeHealth();
      if (!wasBefore && backendReady) {
        startMonitorPolling();
      } else if (wasBefore && !backendReady) {
        stopMonitorPolling();
      }
    }, 2000);
  }

  function handleSetupComplete() {
    setupComplete = true;
    init();
  }

  onMount(() => {
    getVersion().then((v) => { appVersion = v; }).catch(() => {});

    unsubIpcMessages = agentIpcMessages.subscribe((ipcMsgs) => {
      if (ipcMsgs.length > lastIpcCount) {
        const newMsgs = ipcMsgs.slice(lastIpcCount);
        lastIpcCount = ipcMsgs.length;
        for (const msg of newMsgs) {
          const label = msg.sourceGroup && msg.sourceGroup !== groupId ? `[${msg.sourceGroup}] ` : '';
          messages = [...messages, { role: 'agent', text: `${label}${msg.text}` }];
        }
      }
    });

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
      if (unsubIpcMessages) {
        unsubIpcMessages();
      }
      stopMonitorPolling();
    };
  });

  // --- Chat ---
  async function handleSend(text: string) {
    if (streaming || !backendReady) return;

    // Push user message to history
    messages = [...messages, { role: 'user', text }];
    streaming = true;
    streamText = "";

    let agentReply = "";

    try {
      for await (const event of streamChat(text, groupId)) {
        if (event.type === "message" && event.data.text) {
          streamText += event.data.text;
        } else if (event.type === "error") {
          const errorText = event.data.error || "An error occurred";
          agentReply = streamText
            ? streamText + `\n\nError: ${errorText}`
            : `Error: ${errorText}`;
          streamText = "";
          break;
        } else if (event.type === "done") {
          break;
        }
      }
    } catch (e: unknown) {
      const errorText = e instanceof Error ? e.message : "Connection failed";
      agentReply = `Error: ${errorText}`;
      const healthy = await checkHealth();
      backendReady = healthy;
      backendStarting = false;
    }

    // Commit streamed text as complete
    if (streamText) {
      agentReply = streamText;
    }

    // Push agent reply to history
    if (agentReply) {
      messages = [...messages, { role: 'agent', text: agentReply }];
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

  function handleDrag(e: MouseEvent) {
    // Don't drag when clicking buttons or interactive elements
    const target = e.target as HTMLElement;
    if (target.closest("button, a, input, [data-no-drag]")) return;
    if (e.button === 0 && e.detail === 1) {
      getCurrentWindow().startDragging();
    }
  }

  function handleDragDblClick(e: MouseEvent) {
    const target = e.target as HTMLElement;
    if (target.closest("button, a, input, [data-no-drag]")) return;
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
  <div class="app">
    <!-- Top bar: drag region + controls -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="topbar" class:macos={isMac} class:windows={!isMac} onmousedown={handleDrag} ondblclick={handleDragDblClick}>
      <span class="topbar-title">NanoClaw</span>
      <div class="topbar-spacer"></div>
      <button class="gear-btn" onclick={() => { showSettings = true; }} title="Settings" >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/>
          <circle cx="12" cy="12" r="3"/>
        </svg>
      </button>
      {#if appVersion}<span class="version">v{appVersion}</span>{/if}
    </div>

    <!-- Main area: canvas + side panel -->
    <div class="main-area">
      <div class="canvas-area">
        <CanvasView
          status={monitorStatus}
          containers={monitorContainers}
          tasks={monitorTasks}
          pilot={monitorPilot}
          selectedId={panelView.type === 'local' ? 'local' : panelView.type === 'peer' ? canonicalizeNodeId(panelView.peer.id) : panelView.type === 'container' ? panelView.container.name : null}
          onSelectLocal={openLocalPanel}
          onSelectPeer={openPeerPanel}
          onSelectContainer={openContainerPanel}
          onCanvasClick={resetToLocal}
        />
      </div>

      <DetailPanel>
        {#if panelView.type === "local"}
          <LocalDetail
            status={monitorStatus}
            containers={monitorContainers}
            backendStatus={status}
            {chatState}
            {messages}
            {streaming}
            {streamText}
            disabled={!backendReady || streaming}
            onSend={handleSend}
            bind:inputRef
          />
        {:else if panelView.type === "peer"}
          <PeerDetail peer={panelView.peer} />
        {:else if panelView.type === "container"}
          <ContainerDetail container={panelView.container} />
        {/if}
      </DetailPanel>
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
    position: relative;
  }

  .topbar {
    height: 40px;
    min-height: 40px;
    display: flex;
    align-items: center;
    padding: 0 12px;
    gap: 8px;
    z-index: 5;
    -webkit-app-region: drag;
    cursor: default;
    user-select: none;
    border-bottom: 1px solid rgba(var(--accent-rgb), 0.2);
    box-shadow: 0 1px var(--glow-spread) rgba(var(--accent-rgb), calc(var(--glow-opacity) * 0.2));
  }

  /* macOS: traffic lights on the left (~78px) */
  .topbar.macos {
    padding-left: 78px;
  }

  /* Windows: native controls on the right (~140px) */
  .topbar.windows {
    padding-right: 140px;
  }

  .topbar-title {
    position: absolute;
    left: 50%;
    transform: translateX(-50%);
    font-size: 12px;
    font-weight: 600;
    color: var(--accent);
    opacity: 0.7;
    pointer-events: none;
    letter-spacing: 2px;
    text-transform: uppercase;
    text-shadow: 0 0 var(--glow-spread) rgba(var(--accent-rgb), var(--glow-opacity));
  }

  .topbar-spacer {
    flex: 1;
  }

  .gear-btn {
    padding: 6px;
    border-radius: 6px;
    color: var(--text-muted);
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    -webkit-app-region: no-drag;
  }

  .gear-btn:hover {
    background: rgba(255, 255, 255, 0.06);
    color: var(--text);
  }

  .version {
    font-size: 11px;
    color: var(--text-muted);
    opacity: 0.6;
    user-select: none;
    -webkit-app-region: no-drag;
  }

  .main-area {
    flex: 1;
    min-height: 0;
    display: flex;
  }

  .canvas-area {
    flex: 1;
    min-width: 0;
    min-height: 0;
  }
</style>

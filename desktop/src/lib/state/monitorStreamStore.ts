import { writable } from 'svelte/store';

import { getMonitorEventsUrl } from '../api';
import { pilotEventStore, type MonitorSseEvent } from './pilotEventStore';

interface MonitorStreamState {
  connected: boolean;
  reconnecting: boolean;
  lastError: string | null;
}

const initialState: MonitorStreamState = {
  connected: false,
  reconnecting: false,
  lastError: null,
};

const RECONNECT_DELAY_MS = 5000;

const store = writable<MonitorStreamState>(initialState);

let source: EventSource | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let active = false;
let lastEventId: string | null = null;

function setState(patch: Partial<MonitorStreamState>): void {
  store.update((state) => ({ ...state, ...patch }));
}

function handleMessage(event: MessageEvent): void {
  try {
    const payload = JSON.parse(event.data) as MonitorSseEvent;
    if (typeof payload.id === 'string' && payload.id.length > 0) {
      lastEventId = payload.id;
    }
    pilotEventStore.ingestMonitorEvent(payload);
  } catch {
    // Ignore malformed monitor event payloads.
  }
}

function attachPilotListeners(nextSource: EventSource): void {
  nextSource.addEventListener('pilot.node_event', handleMessage as EventListener);
}

function clearReconnectTimer(): void {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
}

function scheduleReconnect(): void {
  if (!active || reconnectTimer) return;

  setState({ reconnecting: true });
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    connect();
  }, RECONNECT_DELAY_MS);
}

function connect(): void {
  if (!active || source) return;

  const nextSource = new EventSource(getMonitorEventsUrl(lastEventId));
  source = nextSource;

  attachPilotListeners(nextSource);

  nextSource.onopen = () => {
    setState({ connected: true, reconnecting: false, lastError: null });
  };

  nextSource.onerror = () => {
    setState({ connected: false, lastError: 'Monitor event stream disconnected' });
    nextSource.close();
    if (source === nextSource) {
      source = null;
    }
    scheduleReconnect();
  };
}

function stopInternal(): void {
  active = false;
  clearReconnectTimer();
  lastEventId = null;

  if (source) {
    source.close();
    source = null;
  }

  store.set(initialState);
}

export const monitorStreamStore = {
  subscribe: store.subscribe,

  start(): void {
    if (active) return;
    active = true;
    clearReconnectTimer();
    connect();
  },

  stop(): void {
    stopInternal();
  },
};

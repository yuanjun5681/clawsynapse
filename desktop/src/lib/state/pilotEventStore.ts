import { derived, writable, type Readable } from 'svelte/store';

import {
  canonicalizeNodeId,
  normalizePilotWebhookEvent,
  type NodeEvent,
  type PilotWebhookPayload,
} from '../domain/pilot/pilot-events';
import { chatBubbleStore } from './chatBubbleStore';
import { pilotGraphStore } from './pilotGraphStore';

export interface MonitorSseEvent {
  id?: string;
  type: string;
  timestamp: string;
  data: Record<string, unknown>;
}

interface PilotEventState {
  eventsByNodeId: Record<string, NodeEvent[]>;
  globalRecentEvents: NodeEvent[];
  unreadByNodeId: Record<string, number>;
  lastEventId: string | null;
}

const MAX_EVENTS_PER_NODE = 100;
const MAX_GLOBAL_EVENTS = 300;

const initialState: PilotEventState = {
  eventsByNodeId: {},
  globalRecentEvents: [],
  unreadByNodeId: {},
  lastEventId: null,
};

const seenEventIds = new Set<string>();
const store = writable<PilotEventState>(initialState);

function ensureObjectRecord(value: unknown): Record<string, unknown> {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>;
  }
  return {};
}

function parseNodeId(value: unknown): number | string | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  if (typeof value === 'string' && value.trim().length > 0) return value;
  return undefined;
}

function toPilotWebhookPayload(event: MonitorSseEvent): PilotWebhookPayload | null {
  if (event.type === 'pilot.node_event') {
    const data = ensureObjectRecord(event.data);
    return {
      event: typeof data.event === 'string' ? data.event : undefined,
      node_id: parseNodeId(data.node_id),
      timestamp: typeof data.timestamp === 'string' ? data.timestamp : event.timestamp,
      data: ensureObjectRecord(data.data),
    };
  }

  if (event.type === 'pilot.message') {
    const data = ensureObjectRecord(event.data);
    return {
      event: typeof data.event === 'string' ? data.event : undefined,
      node_id: parseNodeId(data.nodeId),
      timestamp: event.timestamp,
      data: ensureObjectRecord(data.data),
    };
  }

  return null;
}

function upsertEvent(state: PilotEventState, event: NodeEvent): PilotEventState {
  const normalizedState = normalizeStateKeys(state);

  if (seenEventIds.has(event.id)) {
    return normalizedState;
  }
  seenEventIds.add(event.id);

  const nodeEvents = normalizedState.eventsByNodeId[event.nodeIdForCanvas] ?? [];
  const duplicated = nodeEvents.some((existing) => {
    return (
      existing.kind === event.kind &&
      existing.ts === event.ts &&
      existing.summary === event.summary
    );
  });
  if (duplicated) {
    return normalizedState;
  }
  const nextNodeEvents = [event, ...nodeEvents].slice(0, MAX_EVENTS_PER_NODE);

  return {
    eventsByNodeId: {
      ...normalizedState.eventsByNodeId,
      [event.nodeIdForCanvas]: nextNodeEvents,
    },
    globalRecentEvents: [event, ...normalizedState.globalRecentEvents].slice(0, MAX_GLOBAL_EVENTS),
    unreadByNodeId: {
      ...normalizedState.unreadByNodeId,
      [event.nodeIdForCanvas]:
        (normalizedState.unreadByNodeId[event.nodeIdForCanvas] ?? 0) + 1,
    },
    lastEventId: event.id,
  };
}

function normalizeStateKeys(state: PilotEventState): PilotEventState {
  const normalizedEventsByNodeId: Record<string, NodeEvent[]> = {};
  for (const [rawNodeId, events] of Object.entries(state.eventsByNodeId)) {
    const normalizedNodeId = canonicalizeNodeId(rawNodeId);
    const existing = normalizedEventsByNodeId[normalizedNodeId] ?? [];
    normalizedEventsByNodeId[normalizedNodeId] = [...existing, ...events].slice(
      0,
      MAX_EVENTS_PER_NODE,
    );
  }

  const normalizedUnreadByNodeId: Record<string, number> = {};
  for (const [rawNodeId, unreadCount] of Object.entries(state.unreadByNodeId)) {
    const normalizedNodeId = canonicalizeNodeId(rawNodeId);
    normalizedUnreadByNodeId[normalizedNodeId] =
      (normalizedUnreadByNodeId[normalizedNodeId] ?? 0) + unreadCount;
  }

  return {
    ...state,
    eventsByNodeId: normalizedEventsByNodeId,
    unreadByNodeId: normalizedUnreadByNodeId,
  };
}

function extractBubbleText(event: NodeEvent): string {
  const data = event.raw.data ?? {};
  for (const key of ['content', 'message', 'text', 'body', 'value']) {
    const value = data[key];
    if (typeof value === 'string' && value.trim().length > 0) {
      return value;
    }
  }
  return '';
}

export const pilotEventStore = {
  subscribe: store.subscribe,

  ingestMonitorEvent(event: MonitorSseEvent): void {
    const payload = toPilotWebhookPayload(event);
    if (!payload) return;

    const normalized = normalizePilotWebhookEvent(payload);
    pilotGraphStore.ensureNodeExists(normalized.nodeIdForCanvas);

    store.update((state) => upsertEvent(state, normalized));

    // Trigger chat bubble for message events
    if (normalized.kind === 'message.sent' || normalized.kind === 'message.received') {
      const direction = normalized.kind === 'message.sent' ? 'sent' : 'received';
      const content = extractBubbleText(normalized);
      if (content) {
        chatBubbleStore.add(normalized.nodeIdForCanvas, content, direction);
      }
    }
  },

  markNodeRead(nodeId: string): void {
    if (!nodeId) return;
    store.update((state) => ({
      ...state,
      unreadByNodeId: {
        ...state.unreadByNodeId,
        [nodeId]: 0,
      },
    }));
  },

  clear(): void {
    seenEventIds.clear();
    store.set(initialState);
  },
};

export function selectNodeEvents(nodeId: string): Readable<NodeEvent[]> {
  return derived(store, ($state) => $state.eventsByNodeId[nodeId] ?? []);
}

export function selectNodeUnread(nodeId: string): Readable<number> {
  return derived(store, ($state) => $state.unreadByNodeId[nodeId] ?? 0);
}

export function selectNodeLastEvent(nodeId: string): Readable<NodeEvent | null> {
  return derived(store, ($state) => {
    const events = $state.eventsByNodeId[nodeId] ?? [];
    return events.length > 0 ? events[0] : null;
  });
}

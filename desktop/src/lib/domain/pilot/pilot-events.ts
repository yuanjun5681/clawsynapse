import {
  buildPilotEventSummary,
  classifyPilotEventKind,
  isPilotActionRequired,
  resolvePeerNodeIdFromData,
} from './pilot-format';

export type NodeEventKind =
  | 'handshake.received'
  | 'message.received'
  | 'message.sent'
  | 'data.file'
  | 'unknown';

export type NodeSeverity = 'info' | 'warn';

export interface PilotWebhookData {
  peer_node_id?: number | string;
  peer?: number | string;
  message?: string;
  content?: string;
  justification?: string;
  filename?: string;
  [key: string]: unknown;
}

export interface PilotWebhookPayload {
  event?: string;
  node_id?: number | string;
  timestamp?: string;
  data?: PilotWebhookData;
}

export interface NodeEvent {
  id: string;
  ts: string;
  kind: NodeEventKind;
  nodeIdForCanvas: string;
  receiverNodeId?: string;
  peerNodeId?: string;
  summary: string;
  severity: NodeSeverity;
  actionRequired: boolean;
  raw: PilotWebhookPayload;
  schemaVersion: 1;
}

function toNodeId(value: unknown): string | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) {
    return String(value);
  }
  if (typeof value === 'string' && value.trim().length > 0) {
    return value;
  }
  return undefined;
}

export function canonicalizeNodeId(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) return 'unknown';

  const nodePrefix = trimmed.match(/^node-(\d+)$/i);
  if (nodePrefix) {
    return String(Number.parseInt(nodePrefix[1], 10));
  }

  if (/^\d+$/.test(trimmed)) {
    return String(Number.parseInt(trimmed, 10));
  }

  const dottedHex = trimmed.match(/^0:[0-9a-f]{4}\.[0-9a-f]{4}\.([0-9a-f]{4})$/i);
  if (dottedHex) {
    return String(Number.parseInt(dottedHex[1], 16));
  }

  return trimmed;
}

function createNodeEventId(kind: NodeEventKind, ts: string, nodeIdForCanvas: string): string {
  const base = `${ts}:${kind}:${nodeIdForCanvas}`;
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return `${base}:${crypto.randomUUID()}`;
  }
  return `${base}:${Math.random().toString(36).slice(2, 10)}`;
}

export function resolveNodeIdForCanvas(payload: PilotWebhookPayload): string {
  const data = payload.data ?? {};
  const nodeId = (
    resolvePeerNodeIdFromData(data) ??
    toNodeId(payload.node_id) ??
    'unknown'
  );
  return canonicalizeNodeId(nodeId);
}

export function normalizePilotWebhookEvent(payload: PilotWebhookPayload): NodeEvent {
  const data = payload.data ?? {};
  const kind = classifyPilotEventKind(payload.event);
  const ts = payload.timestamp ?? new Date().toISOString();
  const nodeIdForCanvas = resolveNodeIdForCanvas(payload);
  const peerNodeIdRaw = resolvePeerNodeIdFromData(data);
  const receiverNodeIdRaw = toNodeId(payload.node_id);
  const peerNodeId = peerNodeIdRaw ? canonicalizeNodeId(peerNodeIdRaw) : undefined;
  const receiverNodeId = receiverNodeIdRaw
    ? canonicalizeNodeId(receiverNodeIdRaw)
    : undefined;

  return {
    id: createNodeEventId(kind, ts, nodeIdForCanvas),
    ts,
    kind,
    nodeIdForCanvas,
    receiverNodeId,
    peerNodeId,
    summary: buildPilotEventSummary(kind, data),
    severity: kind === 'handshake.received' ? 'warn' : 'info',
    actionRequired: isPilotActionRequired(kind),
    raw: payload,
    schemaVersion: 1,
  };
}

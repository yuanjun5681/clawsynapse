import type { NodeEventKind, PilotWebhookData } from './pilot-events';

export function classifyPilotEventKind(eventName: string | undefined): NodeEventKind {
  if (eventName === 'handshake.received') return 'handshake.received';
  if (eventName === 'message.received' || eventName === 'data.message') return 'message.received';
  if (eventName === 'file.received' || eventName === 'data.file') return 'data.file';
  return 'unknown';
}

export function isPilotActionRequired(kind: NodeEventKind): boolean {
  return kind === 'handshake.received';
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

export function resolvePeerNodeIdFromData(data: PilotWebhookData): string | undefined {
  const direct =
    toNodeId(data.peer_node_id) ??
    toNodeId(data.peer) ??
    toNodeId(data.from_node_id) ??
    toNodeId(data.from) ??
    toNodeId(data.sender_node_id) ??
    toNodeId(data.source_node_id) ??
    toNodeId(data.sender);
  if (direct) return direct;

  const fromObj = data.from;
  if (fromObj && typeof fromObj === 'object' && !Array.isArray(fromObj)) {
    const record = fromObj as Record<string, unknown>;
    return toNodeId(record.node_id) ?? toNodeId(record.id);
  }

  return undefined;
}

export function buildPilotEventSummary(kind: NodeEventKind, data: PilotWebhookData): string {
  const peerNodeId = resolvePeerNodeIdFromData(data) ?? 'unknown';

  if (kind === 'message.received') {
    const content = extractMessageContent(data);
    if (content) return `Message from node ${String(peerNodeId)}: ${content}`;
    return `Message from node ${String(peerNodeId)} (no message text field in webhook payload)`;
  }

  if (kind === 'data.file') {
    const filename = typeof data.filename === 'string'
      ? data.filename
      : typeof data.name === 'string'
        ? data.name
        : typeof data.path === 'string'
          ? data.path
          : 'unknown file';
    return `File from node ${String(peerNodeId)}: ${filename}`;
  }

  if (kind === 'handshake.received') {
    const justification = typeof data.justification === 'string'
      ? data.justification
      : '';
    return `Handshake request from node ${String(peerNodeId)}${justification ? `: ${justification}` : ''}`;
  }

  return 'Unsupported pilot event';
}

function extractMessageContent(data: PilotWebhookData): string {
  const directFields = ['message', 'content', 'text', 'body', 'value'];
  for (const field of directFields) {
    const value = data[field];
    if (typeof value === 'string' && value.trim().length > 0) {
      return value;
    }
  }

  const nestedFields = ['payload', 'data', 'message'];
  for (const field of nestedFields) {
    const nested = data[field];
    if (!nested || typeof nested !== 'object' || Array.isArray(nested)) continue;
    const record = nested as Record<string, unknown>;
    for (const key of ['message', 'content', 'text', 'body', 'value']) {
      const nestedValue = record[key];
      if (typeof nestedValue === 'string' && nestedValue.trim().length > 0) {
        return nestedValue;
      }
    }
  }

  return '';
}

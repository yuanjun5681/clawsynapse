import type { NodeEventKind, PilotWebhookData } from './pilot-events';

const KNOWN_EVENTS: Set<string> = new Set([
  'handshake.received', 'handshake.pending', 'handshake.approved',
  'handshake.rejected', 'handshake.auto_approved',
  'message.received', 'message.sent',
  'data.file', 'data.datagram',
  'node.registered', 'node.reregistered', 'node.deregistered',
  'conn.syn_received', 'conn.established', 'conn.fin', 'conn.rst', 'conn.idle_timeout',
  'tunnel.peer_added', 'tunnel.established', 'tunnel.relay_activated',
  'trust.revoked', 'trust.revoked_by_peer',
  'pubsub.subscribed', 'pubsub.unsubscribed', 'pubsub.published',
  'security.syn_rate_limited', 'security.nonce_replay',
]);

export function classifyPilotEventKind(eventName: string | undefined): NodeEventKind {
  if (!eventName) return 'unknown';
  // aliases
  if (eventName === 'data.message') return 'message.received';
  if (eventName === 'file.received') return 'data.file';
  if (KNOWN_EVENTS.has(eventName)) return eventName as NodeEventKind;
  return 'unknown';
}

export function isPilotActionRequired(kind: NodeEventKind): boolean {
  return kind === 'handshake.received' || kind === 'handshake.pending';
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

  if (kind === 'message.sent') {
    const content = extractMessageContent(data);
    if (content) return `Sent to node ${String(peerNodeId)}: ${content}`;
    return `Sent message to node ${String(peerNodeId)}`;
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

  if (kind === 'handshake.received' || kind === 'handshake.pending') {
    const justification = typeof data.justification === 'string'
      ? data.justification
      : '';
    return `Handshake request from node ${String(peerNodeId)}${justification ? `: ${justification}` : ''}`;
  }

  if (kind === 'handshake.approved' || kind === 'handshake.auto_approved')
    return `Handshake approved with node ${String(peerNodeId)}`;
  if (kind === 'handshake.rejected')
    return `Handshake rejected for node ${String(peerNodeId)}`;
  if (kind === 'trust.revoked')
    return `Trust revoked for node ${String(peerNodeId)}`;
  if (kind === 'trust.revoked_by_peer')
    return `Trust revoked by node ${String(peerNodeId)}`;

  if (kind === 'node.registered') return 'Node registered';
  if (kind === 'node.reregistered') return 'Node re-registered';
  if (kind === 'node.deregistered') return 'Node deregistered';

  if (kind === 'conn.syn_received') return `Connection request from node ${String(peerNodeId)}`;
  if (kind === 'conn.established') return `Connection established with node ${String(peerNodeId)}`;
  if (kind === 'conn.fin') return `Connection closed with node ${String(peerNodeId)}`;
  if (kind === 'conn.rst') return `Connection reset with node ${String(peerNodeId)}`;
  if (kind === 'conn.idle_timeout') return `Connection idle timeout with node ${String(peerNodeId)}`;

  if (kind === 'tunnel.peer_added') return `Tunnel peer discovered: node ${String(peerNodeId)}`;
  if (kind === 'tunnel.established') return `Tunnel established with node ${String(peerNodeId)}`;
  if (kind === 'tunnel.relay_activated') return `Relay activated for node ${String(peerNodeId)}`;

  if (kind === 'data.datagram') return `Datagram from node ${String(peerNodeId)}`;

  if (kind === 'pubsub.subscribed') return 'Subscribed to topic';
  if (kind === 'pubsub.unsubscribed') return 'Unsubscribed from topic';
  if (kind === 'pubsub.published') return 'Published to topic';

  if (kind === 'security.syn_rate_limited') return 'SYN rate limiter triggered';
  if (kind === 'security.nonce_replay') return 'Nonce replay detected';

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

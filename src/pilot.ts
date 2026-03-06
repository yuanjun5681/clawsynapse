import { execSync } from 'child_process';
import http from 'node:http';

import {
  ASSISTANT_NAME,
  MAIN_GROUP_FOLDER,
} from './config.js';
import { emitMonitorEvent } from './monitor-events.js';
import { RegisteredGroup } from './types.js';
import { logger } from './logger.js';

export interface PilotInboxMessageNormalized {
  from: string;
  content: string;
  timestamp: string;
  type?: string;
  raw: Record<string, unknown>;
}

export function canonicalizePilotNodeId(value: string): string {
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

function textFromUnknown(value: unknown): string {
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') {
    return String(value);
  }
  if (value && typeof value === 'object') {
    try {
      return JSON.stringify(value);
    } catch {
      return '';
    }
  }
  return '';
}

function pickText(
  record: Record<string, unknown>,
  keys: string[],
): string {
  for (const key of keys) {
    const value = record[key];
    const text = textFromUnknown(value).trim();
    if (text.length > 0) return text;
  }
  return '';
}

function normalizePilotInboxEntry(
  entry: unknown,
): PilotInboxMessageNormalized | null {
  if (!entry || typeof entry !== 'object' || Array.isArray(entry)) return null;
  const record = entry as Record<string, unknown>;
  const payload =
    record.payload && typeof record.payload === 'object' && !Array.isArray(record.payload)
      ? (record.payload as Record<string, unknown>)
      : null;

  const from = pickText(record, ['from', 'peer', 'peer_node_id', 'node_id', 'sender']);
  const content =
    pickText(record, ['content', 'message', 'text', 'body', 'data']) ||
    (payload ? pickText(payload, ['content', 'message', 'text', 'body', 'data']) : '');
  const timestamp =
    pickText(record, ['timestamp', 'received_at', 'created_at', 'time']) ||
    new Date().toISOString();
  const type = pickText(record, ['type']) || undefined;

  if (!from && !content) return null;
  return {
    from: from || 'unknown',
    content,
    timestamp,
    type,
    raw: record,
  };
}

export function readPilotInboxMessages(): {
  messages: PilotInboxMessageNormalized[];
  rawEntries: unknown[];
} {
  try {
    const output = execSync('pilotctl --json inbox', {
      encoding: 'utf-8',
      timeout: 5000,
    });
    const payload = JSON.parse(output) as unknown;
    let entries: unknown[] = [];
    if (Array.isArray(payload)) {
      entries = payload;
    } else if (payload && typeof payload === 'object') {
      const root = payload as Record<string, unknown>;
      if (Array.isArray(root.messages)) {
        entries = root.messages;
      } else if (Array.isArray(root.data)) {
        entries = root.data;
      } else if (
        root.data &&
        typeof root.data === 'object' &&
        !Array.isArray(root.data)
      ) {
        const dataObj = root.data as Record<string, unknown>;
        if (Array.isArray(dataObj.messages)) {
          entries = dataObj.messages;
        }
      }
    }

    const messages = entries
      .map((entry) => normalizePilotInboxEntry(entry))
      .filter((entry): entry is NonNullable<typeof entry> => entry !== null);

    return { messages, rawEntries: entries };
  } catch {
    return { messages: [], rawEntries: [] };
  }
}

export interface PilotWebhookDeps {
  parseBody: (req: http.IncomingMessage) => Promise<string>;
  jsonResponse: (res: http.ServerResponse, status: number, data: unknown) => void;
  registeredGroups: () => Record<string, RegisteredGroup>;
  registerGroup: (jid: string, group: RegisteredGroup) => void;
  queue: {
    sendMessage(jid: string, msg: string): boolean;
    enqueueMessageCheck(jid: string): void;
  };
  pendingPrompts: Map<string, string>;
}

export function createPilotWebhookHandler(
  deps: PilotWebhookDeps,
): (req: http.IncomingMessage, res: http.ServerResponse) => Promise<void> {
  return async (req, res) => {
    const toNodeId = (value: unknown): string | undefined => {
      if (typeof value === 'number' && Number.isFinite(value)) return String(value);
      if (typeof value === 'string' && value.trim().length > 0) return value;
      return undefined;
    };
    const resolvePeerNodeId = (value: Record<string, unknown>): string => {
      const direct =
        toNodeId(value.peer_node_id) ??
        toNodeId(value.peer) ??
        toNodeId(value.from_node_id) ??
        toNodeId(value.from) ??
        toNodeId(value.sender_node_id) ??
        toNodeId(value.source_node_id) ??
        toNodeId(value.sender);
      if (direct) return direct;

      const fromObj = value.from;
      if (fromObj && typeof fromObj === 'object' && !Array.isArray(fromObj)) {
        const fromRecord = fromObj as Record<string, unknown>;
        return toNodeId(fromRecord.node_id) ?? toNodeId(fromRecord.id) ?? 'unknown';
      }

      return 'unknown';
    };

    // Only accept from localhost
    const remoteAddr = req.socket.remoteAddress;
    if (
      remoteAddr !== '127.0.0.1' &&
      remoteAddr !== '::1' &&
      remoteAddr !== '::ffff:127.0.0.1'
    ) {
      deps.jsonResponse(res, 403, { error: 'Forbidden' });
      return;
    }

    let body: string;
    try {
      body = await deps.parseBody(req);
    } catch (err) {
      if (err instanceof Error && err.message === 'REQUEST_BODY_TOO_LARGE') {
        deps.jsonResponse(res, 413, { error: 'Request body too large' });
        return;
      }
      deps.jsonResponse(res, 400, { error: 'Invalid request body' });
      return;
    }

    let payload: {
      event?: string;
      node_id?: number;
      timestamp?: string;
      data?: Record<string, unknown>;
    };
    try {
      payload = JSON.parse(body);
    } catch {
      deps.jsonResponse(res, 400, { error: 'Invalid JSON' });
      return;
    }

    const event = payload.event;
    const data = payload.data || {};
    const timestamp = payload.timestamp || new Date().toISOString();
    let enrichedData: Record<string, unknown> = data;

    logger.info({ event, nodeId: payload.node_id }, 'Pilot webhook received');
    if (event === 'message.received') {
      logger.info(
        {
          nodeId: payload.node_id,
          dataKeys: Object.keys(data),
          from: data.from ?? data.peer_node_id ?? data.peer ?? null,
          type: data.type ?? null,
        },
        'Pilot message webhook payload shape',
      );
    }
    let prompt: string | null = null;

    if (event === 'message.received' || event === 'data.message') {
      const peer = resolvePeerNodeId(data);
      let content: string =
        [data.message, data.content, data.text]
          .map((value) => (typeof value === 'string' ? value : ''))
          .find((value) => value.trim().length > 0) ?? '';
      if (!content) {
        const targetCanonical = canonicalizePilotNodeId(peer);
        const { messages } = readPilotInboxMessages();
        const latest = messages
          .filter((message) => {
            const fromCanonical = canonicalizePilotNodeId(message.from);
            return fromCanonical === targetCanonical || message.from === peer;
          })
          .sort((a, b) => b.timestamp.localeCompare(a.timestamp))[0];
        if (latest?.content) {
          content = latest.content;
          enrichedData = {
            ...data,
            content,
            content_source: 'inbox',
          };
          logger.info(
            { peer, contentLength: content.length },
            'Pilot message enriched from inbox',
          );
        }
      }
      if (!content && typeof data.type === 'string') {
        content = data.type;
      }
      prompt = `[Pilot Protocol] 收到来自 node ${peer} 的消息: ${content}`;
    } else if (event === 'file.received' || event === 'data.file') {
      const peer = resolvePeerNodeId(data);
      prompt = `[Pilot Protocol] 收到来自 node ${peer} 的文件，请运行 pilotctl received 查看`;
    } else if (event === 'handshake.received') {
      const peer = resolvePeerNodeId(data);
      const justification = data.justification ?? '';
      prompt = `[Pilot Protocol] node ${peer} 请求建立信任连接，理由: ${justification}。请决定是否 approve 或 reject`;
    } else {
      logger.debug({ event }, 'Pilot webhook event ignored (no handler)');
    }

    emitMonitorEvent('pilot.node_event', {
      event,
      node_id: payload.node_id,
      timestamp,
      data: enrichedData,
    });
    emitMonitorEvent('pilot.message', {
      event,
      nodeId: payload.node_id,
      data: enrichedData,
    });

    if (prompt) {
      const chatJid = MAIN_GROUP_FOLDER;
      const groups = deps.registeredGroups();

      if (!groups[chatJid]) {
        deps.registerGroup(chatJid, {
          name: 'Main',
          folder: MAIN_GROUP_FOLDER,
          trigger: `@${ASSISTANT_NAME}`,
          added_at: new Date().toISOString(),
          requiresTrigger: false,
        });
      }

      if (!deps.queue.sendMessage(chatJid, prompt)) {
        deps.pendingPrompts.set(chatJid, prompt);
        deps.queue.enqueueMessageCheck(chatJid);
      }

      logger.info({ event, chatJid }, 'Pilot webhook routed to main group');
    }

    deps.jsonResponse(res, 200, { status: 'ok' });
  };
}

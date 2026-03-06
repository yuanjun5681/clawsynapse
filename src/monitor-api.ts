import { execSync } from 'child_process';
import http from 'node:http';

import { HTTP_PORT } from './config.js';
import {
  getAllTasks,
  getRecentTaskRuns,
} from './db.js';
import {
  monitorBus,
  type MonitorEvent,
  emitMonitorEvent,
  getBufferedMonitorEvents,
} from './monitor-events.js';
import { readPilotInboxMessages } from './pilot.js';
import { RegisteredGroup } from './types.js';
import { logger } from './logger.js';

export interface MonitorDeps {
  parseBody: (req: http.IncomingMessage) => Promise<string>;
  jsonResponse: (res: http.ServerResponse, status: number, data: unknown) => void;
  sseWrite: (res: http.ServerResponse, event: string, data: unknown) => void;
  queue: { getStats(): { activeContainers: number; maxContainers: number; waitingGroups: number } };
  registeredGroups: () => Record<string, RegisteredGroup>;
}

export function createMonitorHandlers(deps: MonitorDeps) {
  function handleMonitorStatus(
    _req: http.IncomingMessage,
    res: http.ServerResponse,
  ): void {
    const mem = process.memoryUsage();
    const stats = deps.queue.getStats();
    deps.jsonResponse(res, 200, {
      uptime: process.uptime(),
      memoryMB: Math.round(mem.rss / 1024 / 1024),
      activeContainers: stats.activeContainers,
      maxContainers: stats.maxContainers,
      waitingGroups: stats.waitingGroups,
      registeredGroups: Object.keys(deps.registeredGroups()).length,
      pid: process.pid,
    });
  }

  function handleMonitorContainers(
    _req: http.IncomingMessage,
    res: http.ServerResponse,
  ): void {
    try {
      const output = execSync(
        `docker ps --filter "name=nanoclaw-" --format '{"name":"{{.Names}}","status":"{{.Status}}","created":"{{.CreatedAt}}","image":"{{.Image}}"}'`,
        { encoding: 'utf-8', timeout: 5000 },
      );
      const containers = output
        .trim()
        .split('\n')
        .filter(Boolean)
        .map((line) => {
          const c = JSON.parse(line);
          const match = c.name.match(/^nanoclaw-([^-]+)/);
          return { ...c, groupFolder: match ? match[1] : null };
        });
      deps.jsonResponse(res, 200, containers);
    } catch {
      deps.jsonResponse(res, 200, []);
    }
  }

  function handleMonitorTasks(
    _req: http.IncomingMessage,
    res: http.ServerResponse,
  ): void {
    const tasks = getAllTasks();
    const recentRuns = getRecentTaskRuns(20);
    deps.jsonResponse(res, 200, { tasks, recentRuns });
  }

  function handleMonitorPilot(
    _req: http.IncomingMessage,
    res: http.ServerResponse,
  ): void {
    try {
      interface PilotTrustPeer {
        node_id?: number;
        public_key?: string;
        mutual?: boolean;
      }

      interface PilotTrustResponse {
        data?: {
          trusted?: PilotTrustPeer[];
        };
        status?: string;
      }

      interface PilotPendingPeer {
        node_id?: number;
        public_key?: string;
        name?: string;
        justification?: string;
      }

      interface PilotPendingResponse {
        data?: {
          pending?: PilotPendingPeer[];
        };
        status?: string;
      }

      const infoOutput = execSync('pilotctl --json info', {
        encoding: 'utf-8',
        timeout: 5000,
      });
      const info = JSON.parse(infoOutput);

      let trustedPeers: Array<{
        id: string;
        name: string;
        address: string;
        status: 'online' | 'offline';
      }> = [];
      try {
        const trustOutput = execSync('pilotctl --json trust', {
          encoding: 'utf-8',
          timeout: 5000,
        });
        const trustPayload = JSON.parse(trustOutput) as PilotTrustResponse;
        const trusted = trustPayload.data?.trusted ?? [];
        trustedPeers = trusted
          .filter((peer) => peer.node_id !== undefined)
          .map((peer) => {
            const nodeId = String(peer.node_id);
            return {
              id: nodeId,
              name: `node-${nodeId}`,
              address: peer.public_key ?? '',
              status: peer.mutual ? 'online' : 'offline',
            };
          });
      } catch {
        // trust list may fail if no peers yet
      }

      let pendingHandshakes: Array<{
        id: string;
        name: string;
        justification?: string;
      }> = [];
      try {
        const pendingOutput = execSync('pilotctl --json pending', {
          encoding: 'utf-8',
          timeout: 5000,
        });
        const pendingPayload = JSON.parse(pendingOutput) as PilotPendingResponse;
        const pending = pendingPayload.data?.pending ?? [];
        pendingHandshakes = pending
          .filter((peer) => peer.node_id !== undefined)
          .map((peer) => {
            const nodeId = String(peer.node_id);
            return {
              id: nodeId,
              name: peer.name || `node-${nodeId}`,
              justification: peer.justification,
            };
          });
      } catch {
        // no pending handshakes
      }

      deps.jsonResponse(res, 200, {
        available: true,
        node: info,
        trustedPeers,
        pendingHandshakes,
      });
    } catch {
      deps.jsonResponse(res, 200, { available: false });
    }
  }

  function handleMonitorPilotInbox(
    _req: http.IncomingMessage,
    res: http.ServerResponse,
  ): void {
    const { messages, rawEntries } = readPilotInboxMessages();

    logger.debug(
      {
        inboxEntries: rawEntries.length,
        normalizedMessages: messages.length,
        sampleKeys:
          rawEntries.length > 0 &&
          rawEntries[0] &&
          typeof rawEntries[0] === 'object' &&
          !Array.isArray(rawEntries[0])
            ? Object.keys(rawEntries[0] as Record<string, unknown>)
            : [],
      },
      'Pilot inbox normalized',
    );

    deps.jsonResponse(res, 200, {
      messages,
    });
  }

  async function handleMonitorPilotHandshakeAction(
    req: http.IncomingMessage,
    res: http.ServerResponse,
  ): Promise<void> {
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
      nodeId?: string;
      action?: 'approve' | 'reject';
    };
    try {
      payload = JSON.parse(body);
    } catch {
      deps.jsonResponse(res, 400, { error: 'Invalid JSON' });
      return;
    }

    const nodeId = String(payload.nodeId || '').trim();
    const action = payload.action;
    if (!/^\d+$/.test(nodeId)) {
      deps.jsonResponse(res, 400, { error: 'Invalid nodeId' });
      return;
    }
    if (action !== 'approve' && action !== 'reject') {
      deps.jsonResponse(res, 400, { error: 'Invalid action' });
      return;
    }

    try {
      const command =
        action === 'approve'
          ? `pilotctl approve ${nodeId}`
          : `pilotctl reject ${nodeId}`;
      execSync(command, {
        encoding: 'utf-8',
        timeout: 5000,
      });

      emitMonitorEvent('pilot.handshake.action', {
        nodeId,
        action,
        status: 'ok',
      });
      deps.jsonResponse(res, 200, { status: 'ok' });
    } catch (err) {
      logger.error({ err, nodeId, action }, 'Pilot handshake action failed');
      emitMonitorEvent('pilot.handshake.action', {
        nodeId,
        action,
        status: 'error',
      });
      deps.jsonResponse(res, 500, { error: 'Pilot handshake action failed' });
    }
  }

  function handleMonitorEvents(
    req: http.IncomingMessage,
    res: http.ServerResponse,
  ): void {
    const requestUrl = new URL(req.url || '/', `http://localhost:${HTTP_PORT}`);
    const queryLastEventId = requestUrl.searchParams.get('lastEventId') || undefined;
    const headerLastEventId = req.headers['last-event-id'];
    const lastEventId =
      typeof headerLastEventId === 'string' && headerLastEventId.length > 0
        ? headerLastEventId
        : queryLastEventId;

    res.writeHead(200, {
      'Content-Type': 'text/event-stream',
      'Cache-Control': 'no-cache',
      Connection: 'keep-alive',
    });

    const writeEvent = (event: MonitorEvent): void => {
      if (res.writableEnded || res.destroyed) return;
      res.write(
        `id: ${event.id}\nevent: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`,
      );
    };

    const replayEvents = getBufferedMonitorEvents(lastEventId);
    for (const event of replayEvents) {
      writeEvent(event);
    }

    const handler = (event: MonitorEvent) => {
      writeEvent(event);
    };

    monitorBus.onMonitor(handler);

    req.on('close', () => {
      monitorBus.removeListener('monitor', handler);
    });
  }

  return {
    status: handleMonitorStatus,
    containers: handleMonitorContainers,
    tasks: handleMonitorTasks,
    pilot: handleMonitorPilot,
    pilotInbox: handleMonitorPilotInbox,
    pilotHandshakeAction: handleMonitorPilotHandshakeAction,
    events: handleMonitorEvents,
  };
}

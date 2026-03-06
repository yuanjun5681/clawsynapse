import { execSync } from 'child_process';
import http from 'node:http';
import fs from 'fs';
import path from 'path';

import {
  ASSISTANT_NAME,
  API_AUTH_TOKEN,
  DATA_DIR,
  GROUPS_DIR,
  HTTP_HOST,
  HTTP_PORT,
  IDLE_TIMEOUT,
  MAIN_GROUP_FOLDER,
  MAX_REQUEST_BODY_BYTES,
  STORE_DIR,
} from './config.js';
import {
  AvailableGroup,
  ContainerOutput,
  initPilotWebhook,
  runContainerAgent,
  writeGroupsSnapshot,
  writeTasksSnapshot,
} from './container-runner.js';
import {
  getAllRegisteredGroups,
  getAllSessions,
  getAllTasks,
  getRouterState,
  initDatabase,
  setRegisteredGroup,
  setRouterState,
  setSession,
} from './db.js';
import { GroupQueue } from './group-queue.js';
import { startIpcWatcher } from './ipc-watcher.js';
import { createMonitorHandlers } from './monitor-api.js';
import { emitMonitorEvent } from './monitor-events.js';
import { createPilotWebhookHandler } from './pilot.js';
import { startSchedulerLoop } from './task-scheduler.js';
import { RegisteredGroup } from './types.js';
import { logger } from './logger.js';

let sessions: Record<string, string> = {};
let registeredGroups: Record<string, RegisteredGroup> = {};
let lastAgentTimestamp: Record<string, string> = {};

const queue = new GroupQueue();

// --- Output routing ---
// Maps groupId (folder name used as chatJid) to the current SSE response writer.
// When a container produces output, we look here first; if no listener, we buffer.
const outputListeners = new Map<string, (output: ContainerOutput) => void>();
const messageBuffers = new Map<
  string,
  Array<{ text: string; timestamp: string }>
>();
const messageCaptureListeners = new Map<string, Set<(text: string) => void>>();
const pendingPrompts = new Map<string, string>();
// Callbacks fired when a container run completes (event:done)
const completionCallbacks = new Map<string, (sessionId?: string) => void>();
// Only one active SSE request per group to avoid stream collisions.
const activeSseRequests = new Map<string, string>();

function mapTasksForSnapshot(tasks: ReturnType<typeof getAllTasks>): Array<{
  id: string;
  groupFolder: string;
  prompt: string;
  schedule_type: string;
  schedule_value: string;
  status: string;
  next_run: string | null;
}> {
  return tasks.map((t) => ({
    id: t.id,
    groupFolder: t.group_folder,
    prompt: t.prompt,
    schedule_type: t.schedule_type,
    schedule_value: t.schedule_value,
    status: t.status,
    next_run: t.next_run,
  }));
}

function refreshTaskSnapshots(): void {
  const tasksSnapshot = mapTasksForSnapshot(getAllTasks());
  const groupFolders = new Set(
    Object.values(registeredGroups).map((group) => group.folder),
  );

  for (const groupFolder of groupFolders) {
    const isMain = groupFolder === MAIN_GROUP_FOLDER;
    writeTasksSnapshot(groupFolder, isMain, tasksSnapshot);
  }
}

function normalizeGroupId(input: string): string {
  const safe = input
    .replace(/[^a-zA-Z0-9_-]/g, '-')
    .replace(/-+/g, '-')
    .replace(/^-+|-+$/g, '')
    .toLowerCase();

  if (!safe || safe === '.' || safe === '..') {
    throw new Error('Invalid groupId');
  }

  return safe;
}

function loadState(): void {
  const agentTs = getRouterState('last_agent_timestamp');
  try {
    lastAgentTimestamp = agentTs ? JSON.parse(agentTs) : {};
  } catch {
    logger.warn('Corrupted last_agent_timestamp in DB, resetting');
    lastAgentTimestamp = {};
  }
  sessions = getAllSessions();
  registeredGroups = getAllRegisteredGroups();
  logger.info(
    { groupCount: Object.keys(registeredGroups).length },
    'State loaded',
  );
}

function saveState(): void {
  setRouterState('last_agent_timestamp', JSON.stringify(lastAgentTimestamp));
}

function registerGroup(jid: string, group: RegisteredGroup): void {
  registeredGroups[jid] = group;
  setRegisteredGroup(jid, group);

  // Create group folder
  const groupDir = path.join(DATA_DIR, '..', 'groups', group.folder);
  fs.mkdirSync(path.join(groupDir, 'logs'), { recursive: true });

  logger.info(
    { jid, name: group.name, folder: group.folder },
    'Group registered',
  );
}

/**
 * Send a message to the appropriate destination.
 * Routes to SSE listener if one exists, otherwise buffers.
 */
interface SendMessageOptions {
  dropIfNoListener?: boolean;
}

async function sendMessage(
  chatJid: string,
  text: string,
  options?: SendMessageOptions,
): Promise<void> {
  const captureListeners = messageCaptureListeners.get(chatJid);
  if (captureListeners) {
    for (const capture of captureListeners) {
      capture(text);
    }
  }

  const listener = outputListeners.get(chatJid);
  if (listener) {
    listener({
      status: 'success',
      result: text,
    });
  } else {
    if (options?.dropIfNoListener) {
      logger.debug({ chatJid }, 'Message dropped (no SSE listener)');
      return;
    }
    // Buffer for later retrieval
    let buffer = messageBuffers.get(chatJid);
    if (!buffer) {
      buffer = [];
      messageBuffers.set(chatJid, buffer);
    }
    buffer.push({ text, timestamp: new Date().toISOString() });
    logger.debug(
      { chatJid, bufferSize: buffer.length },
      'Message buffered (no SSE listener)',
    );
  }
}

function beginChatOutputCapture(chatJid: string): () => string[] {
  const captured: string[] = [];
  const capture = (text: string) => {
    captured.push(text);
  };

  let listeners = messageCaptureListeners.get(chatJid);
  if (!listeners) {
    listeners = new Set<(text: string) => void>();
    messageCaptureListeners.set(chatJid, listeners);
  }
  listeners.add(capture);

  return () => {
    const current = messageCaptureListeners.get(chatJid);
    if (current) {
      current.delete(capture);
      if (current.size === 0) {
        messageCaptureListeners.delete(chatJid);
      }
    }
    return captured;
  };
}

/**
 * Process a prompt for a group.
 * Replaces processGroupMessages — reads from pendingPrompts map.
 */
async function processPrompt(chatJid: string): Promise<boolean> {
  const group = registeredGroups[chatJid];
  if (!group) return true;

  const prompt = pendingPrompts.get(chatJid);
  pendingPrompts.delete(chatJid);

  if (!prompt) return true;

  logger.info(
    { group: group.name, promptLength: prompt.length },
    'Processing prompt',
  );

  // Track idle timer for closing stdin when agent is idle
  let idleTimer: ReturnType<typeof setTimeout> | null = null;

  const resetIdleTimer = () => {
    if (idleTimer) clearTimeout(idleTimer);
    idleTimer = setTimeout(() => {
      logger.debug(
        { group: group.name },
        'Idle timeout, closing container stdin',
      );
      queue.closeStdin(chatJid);
    }, IDLE_TIMEOUT);
  };

  let hadError = false;

  const output = await runAgent(group, prompt, chatJid, async (result) => {
    if (result.result) {
      const raw =
        typeof result.result === 'string'
          ? result.result
          : JSON.stringify(result.result);
      // Strip <internal>...</internal> blocks — agent uses these for internal reasoning
      const text = raw.replace(/<internal>[\s\S]*?<\/internal>/g, '').trim();
      logger.info({ group: group.name }, `Agent output: ${raw.slice(0, 200)}`);
      if (text) {
        // Route to SSE listener or buffer
        const listener = outputListeners.get(chatJid);
        if (listener) {
          listener({
            status: 'success',
            result: text,
          });
        } else {
          await sendMessage(chatJid, text);
          // Emit monitor event so the desktop TERMINAL can display the output
          // even when no active SSE chat listener exists (e.g. Pilot-triggered runs).
          emitMonitorEvent('agent.ipc_message', {
            chatJid,
            sourceGroup: group.folder,
            text,
          });
        }
      }
      resetIdleTimer();
    } else if (result.status === 'success') {
      // End-of-turn marker from runner.
      const listener = outputListeners.get(chatJid);
      if (listener) {
        listener({
          status: 'success',
          result: null,
          newSessionId: result.newSessionId,
        });
      }
    }

    if (result.status === 'error') {
      hadError = true;
      // Route error to SSE listener
      const listener = outputListeners.get(chatJid);
      if (listener) {
        listener({
          status: 'error',
          result: null,
          error: result.error || 'Agent error',
        });
      }
    }
  });

  if (idleTimer) clearTimeout(idleTimer);

  // Fire completion callback
  const onComplete = completionCallbacks.get(chatJid);
  if (onComplete) {
    completionCallbacks.delete(chatJid);
    onComplete(sessions[group.folder]);
  }

  if (output === 'error' || hadError) {
    logger.warn({ group: group.name }, 'Agent error during prompt processing');
    return false;
  }

  return true;
}

async function runAgent(
  group: RegisteredGroup,
  prompt: string,
  chatJid: string,
  onOutput?: (output: ContainerOutput) => Promise<void>,
): Promise<'success' | 'error'> {
  const isMain = group.folder === MAIN_GROUP_FOLDER;
  const sessionId = sessions[group.folder];

  // Update tasks snapshot for container to read (filtered by group)
  const tasks = getAllTasks();
  writeTasksSnapshot(group.folder, isMain, mapTasksForSnapshot(tasks));

  // Update available groups snapshot (main group only can see all groups)
  const availableGroups = getAvailableGroups();
  writeGroupsSnapshot(
    group.folder,
    isMain,
    availableGroups,
    new Set(Object.keys(registeredGroups)),
  );

  // Wrap onOutput to track session ID from streamed results
  const wrappedOnOutput = onOutput
    ? async (output: ContainerOutput) => {
        if (output.newSessionId) {
          sessions[group.folder] = output.newSessionId;
          setSession(group.folder, output.newSessionId);
        }
        await onOutput(output);
      }
    : undefined;

  try {
    const output = await runContainerAgent(
      group,
      {
        prompt,
        sessionId,
        groupFolder: group.folder,
        chatJid,
        isMain,
      },
      (proc, containerName) =>
        queue.registerProcess(chatJid, proc, containerName, group.folder),
      wrappedOnOutput,
    );

    if (output.newSessionId) {
      sessions[group.folder] = output.newSessionId;
      setSession(group.folder, output.newSessionId);
    }

    if (output.status === 'error') {
      logger.error(
        { group: group.name, error: output.error },
        'Container agent error',
      );
      return 'error';
    }

    return 'success';
  } catch (err) {
    logger.error({ group: group.name, err }, 'Agent error');
    return 'error';
  }
}

/**
 * Get available groups list for the agent.
 */
function getAvailableGroups(): AvailableGroup[] {
  return Object.entries(registeredGroups).map(([jid, group]) => ({
    jid,
    name: group.name,
    lastActivity: group.added_at,
    isRegistered: true,
  }));
}

// --- HTTP API ---

function parseBody(req: http.IncomingMessage): Promise<string> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let total = 0;
    req.on('data', (chunk: Buffer) => {
      total += chunk.length;
      if (total > MAX_REQUEST_BODY_BYTES) {
        reject(new Error('REQUEST_BODY_TOO_LARGE'));
        req.destroy();
        return;
      }
      chunks.push(chunk);
    });
    req.on('end', () => resolve(Buffer.concat(chunks).toString()));
    req.on('error', reject);
  });
}

function jsonResponse(
  res: http.ServerResponse,
  status: number,
  data: unknown,
): void {
  res.writeHead(status, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(data));
}

function sseWrite(
  res: http.ServerResponse,
  event: string,
  data: unknown,
): void {
  if (res.writableEnded || res.destroyed) return;
  res.write(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
}

async function handleChat(
  req: http.IncomingMessage,
  res: http.ServerResponse,
): Promise<void> {
  let body: string;
  try {
    body = await parseBody(req);
  } catch (err) {
    if (err instanceof Error && err.message === 'REQUEST_BODY_TOO_LARGE') {
      jsonResponse(res, 413, { error: 'Request body too large' });
      return;
    }
    jsonResponse(res, 400, { error: 'Invalid request body' });
    return;
  }

  let parsed: { prompt?: string; groupId?: string };
  try {
    parsed = JSON.parse(body);
  } catch {
    jsonResponse(res, 400, { error: 'Invalid JSON' });
    return;
  }

  const prompt = parsed.prompt;
  const rawGroupId =
    typeof parsed.groupId === 'string' && parsed.groupId.trim()
      ? parsed.groupId.trim()
      : MAIN_GROUP_FOLDER;
  let groupId: string;
  try {
    groupId = normalizeGroupId(rawGroupId);
  } catch {
    jsonResponse(res, 400, { error: 'Invalid "groupId" field' });
    return;
  }

  if (!prompt || typeof prompt !== 'string') {
    jsonResponse(res, 400, { error: 'Missing "prompt" field' });
    return;
  }

  // Use the folder name as the chatJid key (same convention as WhatsApp JIDs were used)
  const chatJid = groupId;

  if (activeSseRequests.has(chatJid)) {
    jsonResponse(res, 409, {
      error: 'Another request is already in progress for this group',
    });
    return;
  }

  // Auto-register group if it doesn't exist yet
  if (!registeredGroups[chatJid]) {
    registerGroup(chatJid, {
      name: rawGroupId,
      folder: groupId,
      trigger: `@${ASSISTANT_NAME}`,
      added_at: new Date().toISOString(),
      requiresTrigger: false,
    });
  }

  const requestId = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  activeSseRequests.set(chatJid, requestId);

  // Set up SSE response
  res.writeHead(200, {
    'Content-Type': 'text/event-stream',
    'Cache-Control': 'no-cache',
    Connection: 'keep-alive',
  });

  // Flush any buffered messages first
  const buffered = messageBuffers.get(chatJid);
  if (buffered && buffered.length > 0) {
    for (const msg of buffered) {
      sseWrite(res, 'message', { text: msg.text });
    }
    messageBuffers.delete(chatJid);
  }

  let pipedRequest = false;
  let finished = false;
  const finish = (sessionId?: string) => {
    if (finished) return;
    finished = true;

    sseWrite(res, 'done', { sessionId: sessionId || null });

    if (activeSseRequests.get(chatJid) === requestId) {
      activeSseRequests.delete(chatJid);
      outputListeners.delete(chatJid);
      completionCallbacks.delete(chatJid);
    }
    res.end();
  };

  // Register SSE listener (one active request per group)
  outputListeners.set(chatJid, (output: ContainerOutput) => {
    if (output.status === 'error') {
      sseWrite(res, 'error', { error: output.error || 'Unknown error' });
      finish(output.newSessionId);
    } else if (output.result) {
      sseWrite(res, 'message', { text: output.result });
    } else {
      // Null-result markers denote end of this turn.
      finish(output.newSessionId);
    }
  });

  // Register completion callback
  completionCallbacks.set(chatJid, (sessionId?: string) => {
    finish(sessionId);
  });

  // Clean up on client disconnect
  req.on('close', () => {
    if (activeSseRequests.get(chatJid) === requestId) {
      activeSseRequests.delete(chatJid);
      outputListeners.delete(chatJid);
      completionCallbacks.delete(chatJid);
    }
  });

  // Try piping to active container first
  if (queue.sendMessage(chatJid, prompt)) {
    pipedRequest = true;
    pendingPrompts.delete(chatJid);
    logger.debug({ chatJid }, 'Piped prompt to active container');
  } else {
    // No active container — enqueue for a new one
    pendingPrompts.set(chatJid, prompt);
    queue.enqueueMessageCheck(chatJid);
  }
}

async function handleGetGroups(
  _req: http.IncomingMessage,
  res: http.ServerResponse,
): Promise<void> {
  const groups = Object.entries(registeredGroups).map(([jid, group]) => ({
    id: jid,
    name: group.name,
    folder: group.folder,
    added_at: group.added_at,
  }));
  jsonResponse(res, 200, groups);
}

async function handleCreateGroup(
  req: http.IncomingMessage,
  res: http.ServerResponse,
): Promise<void> {
  let body: string;
  try {
    body = await parseBody(req);
  } catch (err) {
    if (err instanceof Error && err.message === 'REQUEST_BODY_TOO_LARGE') {
      jsonResponse(res, 413, { error: 'Request body too large' });
      return;
    }
    jsonResponse(res, 400, { error: 'Invalid request body' });
    return;
  }

  let parsed: { name?: string; folder?: string };
  try {
    parsed = JSON.parse(body);
  } catch {
    jsonResponse(res, 400, { error: 'Invalid JSON' });
    return;
  }

  const name = parsed.name;
  const folder = parsed.folder || parsed.name;

  if (!name || typeof name !== 'string') {
    jsonResponse(res, 400, { error: 'Missing "name" field' });
    return;
  }

  if (!folder || typeof folder !== 'string') {
    jsonResponse(res, 400, { error: 'Missing "folder" field' });
    return;
  }

  let safeFolder: string;
  try {
    safeFolder = normalizeGroupId(folder);
  } catch {
    jsonResponse(res, 400, { error: 'Invalid "folder" field' });
    return;
  }

  const chatJid = safeFolder;

  if (registeredGroups[chatJid]) {
    jsonResponse(res, 409, { error: 'Group already exists' });
    return;
  }

  registerGroup(chatJid, {
    name,
    folder: safeFolder,
    trigger: `@${ASSISTANT_NAME}`,
    added_at: new Date().toISOString(),
    requiresTrigger: false,
  });

  jsonResponse(res, 201, { id: chatJid, name, folder: safeFolder });
}

function writeFileAtomic(filePath: string, content: string): void {
  const tempPath = `${filePath}.tmp-${process.pid}-${Date.now()}`;
  fs.writeFileSync(tempPath, content, 'utf-8');
  fs.renameSync(tempPath, filePath);
}

async function handleUpdateGroupMemory(
  req: http.IncomingMessage,
  res: http.ServerResponse,
  rawGroupId: string,
): Promise<void> {
  let body: string;
  try {
    body = await parseBody(req);
  } catch (err) {
    if (err instanceof Error && err.message === 'REQUEST_BODY_TOO_LARGE') {
      jsonResponse(res, 413, { error: 'Request body too large' });
      return;
    }
    jsonResponse(res, 400, { error: 'Invalid request body' });
    return;
  }

  let parsed: { content?: string };
  try {
    parsed = JSON.parse(body);
  } catch {
    jsonResponse(res, 400, { error: 'Invalid JSON' });
    return;
  }

  let groupFolder: string;
  try {
    groupFolder = normalizeGroupId(rawGroupId);
  } catch {
    jsonResponse(res, 400, { error: 'Invalid "groupId" field' });
    return;
  }

  if (typeof parsed.content !== 'string') {
    jsonResponse(res, 400, { error: 'Missing "content" field' });
    return;
  }

  const groupDir = path.join(GROUPS_DIR, groupFolder);
  const memoryFile = path.join(groupDir, 'CLAUDE.md');
  const normalizedContent = parsed.content.endsWith('\n')
    ? parsed.content
    : `${parsed.content}\n`;

  try {
    fs.mkdirSync(groupDir, { recursive: true });
    writeFileAtomic(memoryFile, normalizedContent);
  } catch (err) {
    logger.error({ err, groupFolder }, 'Failed to update group memory file');
    jsonResponse(res, 500, { error: 'Failed to save group memory' });
    return;
  }

  jsonResponse(res, 200, {
    status: 'ok',
    groupId: groupFolder,
    path: `groups/${groupFolder}/CLAUDE.md`,
    bytes: Buffer.byteLength(normalizedContent, 'utf-8'),
  });
}

async function handleDeleteSession(
  res: http.ServerResponse,
  folder: string,
): Promise<void> {
  const chatJid = folder;
  const state = queue as any;
  const groupState = state.groups?.get(chatJid);

  if (groupState?.process && !groupState.process.killed) {
    groupState.process.kill('SIGTERM');
    jsonResponse(res, 200, { status: 'stopped' });
  } else {
    jsonResponse(res, 404, { error: 'No active session' });
  }
}

function handleHealth(
  _req: http.IncomingMessage,
  res: http.ServerResponse,
): void {
  jsonResponse(res, 200, { status: 'ok' });
}

function isAuthorized(req: http.IncomingMessage): boolean {
  if (!API_AUTH_TOKEN) return true;

  const auth = req.headers.authorization;
  if (auth === `Bearer ${API_AUTH_TOKEN}`) return true;

  // Support ?token= query param (for EventSource which cannot set headers)
  try {
    const url = new URL(req.url || '/', `http://localhost`);
    if (url.searchParams.get('token') === API_AUTH_TOKEN) return true;
  } catch {
    /* ignore */
  }

  return false;
}

function startHttpServer(): void {
  const handlePilotWebhook = createPilotWebhookHandler({
    parseBody,
    jsonResponse,
    registeredGroups: () => registeredGroups,
    registerGroup,
    queue,
    pendingPrompts,
  });

  const monitor = createMonitorHandlers({
    parseBody,
    jsonResponse,
    sseWrite,
    queue,
    registeredGroups: () => registeredGroups,
  });

  const server = http.createServer(async (req, res) => {
    const url = new URL(req.url || '/', `http://localhost:${HTTP_PORT}`);
    const pathname = url.pathname;
    const method = req.method || 'GET';

    // CORS for desktop app (Tauri WebView)
    const origin = req.headers.origin;
    if (origin) {
      res.setHeader('Access-Control-Allow-Origin', origin);
      res.setHeader(
        'Access-Control-Allow-Methods',
        'GET, POST, DELETE, OPTIONS',
      );
      res.setHeader(
        'Access-Control-Allow-Headers',
        'Content-Type, Authorization',
      );
    }
    if (method === 'OPTIONS') {
      res.writeHead(204);
      res.end();
      return;
    }

    try {
      // Pilot webhook: skip auth (localhost-only check is in handler)
      if (method === 'POST' && pathname === '/api/pilot/webhook') {
        await handlePilotWebhook(req, res);
        return;
      }

      if (pathname !== '/api/health' && !isAuthorized(req)) {
        res.writeHead(401, {
          'Content-Type': 'application/json',
          'WWW-Authenticate': 'Bearer',
        });
        res.end(JSON.stringify({ error: 'Unauthorized' }));
        return;
      }

      // POST /api/chat
      if (method === 'POST' && pathname === '/api/chat') {
        await handleChat(req, res);
        return;
      }

      // GET /api/groups
      if (method === 'GET' && pathname === '/api/groups') {
        await handleGetGroups(req, res);
        return;
      }

      // POST /api/groups
      if (method === 'POST' && pathname === '/api/groups') {
        await handleCreateGroup(req, res);
        return;
      }

      // POST /api/groups/:groupId/memory
      const groupMemoryMatch = pathname.match(
        /^\/api\/groups\/([^/]+)\/memory$/,
      );
      if (method === 'POST' && groupMemoryMatch) {
        await handleUpdateGroupMemory(
          req,
          res,
          decodeURIComponent(groupMemoryMatch[1]),
        );
        return;
      }

      // DELETE /api/groups/:folder/session
      const sessionMatch = pathname.match(/^\/api\/groups\/([^/]+)\/session$/);
      if (method === 'DELETE' && sessionMatch) {
        await handleDeleteSession(res, decodeURIComponent(sessionMatch[1]));
        return;
      }

      // GET /api/health
      if (method === 'GET' && pathname === '/api/health') {
        handleHealth(req, res);
        return;
      }

      // --- Monitor API ---
      if (method === 'GET' && pathname === '/api/monitor/status') {
        monitor.status(req, res);
        return;
      }
      if (method === 'GET' && pathname === '/api/monitor/containers') {
        monitor.containers(req, res);
        return;
      }
      if (method === 'GET' && pathname === '/api/monitor/tasks') {
        monitor.tasks(req, res);
        return;
      }
      if (method === 'GET' && pathname === '/api/monitor/pilot') {
        monitor.pilot(req, res);
        return;
      }
      if (method === 'GET' && pathname === '/api/monitor/pilot/inbox') {
        monitor.pilotInbox(req, res);
        return;
      }
      if (method === 'POST' && pathname === '/api/monitor/pilot/handshake') {
        await monitor.pilotHandshakeAction(req, res);
        return;
      }
      if (method === 'GET' && pathname === '/api/monitor/events') {
        monitor.events(req, res);
        return;
      }

      jsonResponse(res, 404, { error: 'Not found' });
    } catch (err) {
      logger.error({ err, method, pathname }, 'HTTP request error');
      if (!res.headersSent) {
        jsonResponse(res, 500, { error: 'Internal server error' });
      }
    }
  });

  server.listen(HTTP_PORT, HTTP_HOST, () => {
    logger.info({ host: HTTP_HOST, port: HTTP_PORT }, 'HTTP server listening');
  });
}

function ensureContainerRuntimeRunning(): void {
  try {
    execSync('docker info', { stdio: 'pipe' });
    logger.debug('Docker runtime available');
  } catch (err) {
    logger.error({ err }, 'Docker is not running');
    console.error(
      '\n╔════════════════════════════════════════════════════════════════╗',
    );
    console.error(
      '║  FATAL: Docker is not running                                  ║',
    );
    console.error(
      '║                                                                ║',
    );
    console.error(
      '║  Agents cannot run without Docker. To fix:                    ║',
    );
    console.error(
      '║  1. Start Docker Desktop or the Docker daemon                 ║',
    );
    console.error(
      '║  2. Restart NanoClaw                                          ║',
    );
    console.error(
      '╚════════════════════════════════════════════════════════════════╝\n',
    );
    throw new Error('Docker is required but not running');
  }

  // Kill and clean up orphaned NanoClaw containers from previous runs
  try {
    const output = execSync(
      'docker ps --filter "name=nanoclaw-" --format "{{.Names}}"',
      {
        stdio: ['pipe', 'pipe', 'pipe'],
        encoding: 'utf-8',
      },
    );
    const orphans = output.trim().split('\n').filter(Boolean);
    for (const name of orphans) {
      try {
        execSync(`docker stop ${name}`, { stdio: 'pipe' });
      } catch {
        /* already stopped */
      }
    }
    if (orphans.length > 0) {
      logger.info(
        { count: orphans.length, names: orphans },
        'Stopped orphaned containers',
      );
    }
  } catch (err) {
    logger.warn({ err }, 'Failed to clean up orphaned containers');
  }
}

async function main(): Promise<void> {
  ensureContainerRuntimeRunning();
  initDatabase();
  logger.info('Database initialized');
  loadState();

  // Ensure main group exists
  if (!registeredGroups[MAIN_GROUP_FOLDER]) {
    registerGroup(MAIN_GROUP_FOLDER, {
      name: 'Main',
      folder: MAIN_GROUP_FOLDER,
      trigger: `@${ASSISTANT_NAME}`,
      added_at: new Date().toISOString(),
      requiresTrigger: false,
    });
  }

  // Graceful shutdown handlers
  const shutdown = async (signal: string) => {
    logger.info({ signal }, 'Shutdown signal received');
    await queue.shutdown(10000);
    process.exit(0);
  };
  process.on('SIGTERM', () => shutdown('SIGTERM'));
  process.on('SIGINT', () => shutdown('SIGINT'));

  // Set up queue and services
  queue.setProcessMessagesFn(processPrompt);
  startSchedulerLoop({
    registeredGroups: () => registeredGroups,
    getSessions: () => sessions,
    queue,
    refreshTaskSnapshots,
    onProcess: (groupJid, proc, containerName, groupFolder) =>
      queue.registerProcess(groupJid, proc, containerName, groupFolder),
    sendMessage,
    beginChatOutputCapture,
    assistantName: ASSISTANT_NAME,
  });
  startIpcWatcher({
    registeredGroups: () => registeredGroups,
    sendMessage,
    registerGroup,
    refreshTaskSnapshots,
  });
  refreshTaskSnapshots();

  // Start HTTP server
  startHttpServer();

  // Start Pilot Protocol bridge + register webhook (after HTTP server is up)
  initPilotWebhook();

  logger.info(`NanoClaw running (HTTP API on port ${HTTP_PORT})`);
}

main().catch((err) => {
  logger.error({ err }, 'Failed to start NanoClaw');
  process.exit(1);
});

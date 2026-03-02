import { ChildProcess } from 'child_process';
import { CronExpressionParser } from 'cron-parser';
import fs from 'fs';
import path from 'path';

import {
  GROUPS_DIR,
  IDLE_TIMEOUT,
  MAIN_GROUP_FOLDER,
  SCHEDULER_POLL_INTERVAL,
  TASK_COMPLETION_WEBHOOK_URL,
  TIMEZONE,
} from './config.js';
import {
  ContainerOutput,
  runContainerAgent,
  writeTasksSnapshot,
} from './container-runner.js';
import {
  claimTaskForRun,
  getAllTasks,
  getDueTasks,
  getTaskById,
  logTaskRun,
  recoverStuckTasks,
  updateTaskAfterRun,
} from './db.js';
import { GroupQueue } from './group-queue.js';
import { logger } from './logger.js';
import { RegisteredGroup, ScheduledTask } from './types.js';

export interface SchedulerDependencies {
  registeredGroups: () => Record<string, RegisteredGroup>;
  getSessions: () => Record<string, string>;
  queue: GroupQueue;
  refreshTaskSnapshots: () => void;
  onProcess: (
    groupJid: string,
    proc: ChildProcess,
    containerName: string,
    groupFolder: string,
  ) => void;
  sendMessage: (
    jid: string,
    text: string,
    options?: { dropIfNoListener?: boolean },
  ) => Promise<void>;
  beginChatOutputCapture: (jid: string) => () => string[];
  assistantName: string;
}

interface TaskCompletionWebhookPayload {
  taskId: string;
  groupFolder: string;
  chatJid: string;
  scheduleType: ScheduledTask['schedule_type'];
  scheduleValue: string;
  durationMs: number;
  runAt: string;
  nextRun: string | null;
  status: ScheduledTask['status'];
  success: boolean;
  resultSummary: string;
  chatOutput: string | null;
  error: string | null;
}

async function notifyTaskCompletion(
  payload: TaskCompletionWebhookPayload,
): Promise<void> {
  if (!TASK_COMPLETION_WEBHOOK_URL) {
    logger.debug(
      { taskId: payload.taskId },
      'TASK_COMPLETION_WEBHOOK_URL not configured, skipping webhook',
    );
    return;
  }

  try {
    logger.info(
      { taskId: payload.taskId, url: TASK_COMPLETION_WEBHOOK_URL },
      'Sending task completion webhook',
    );
    const response = await fetch(TASK_COMPLETION_WEBHOOK_URL, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
      signal: AbortSignal.timeout(10000),
    });

    if (!response.ok) {
      logger.warn(
        { status: response.status, taskId: payload.taskId },
        'Task completion webhook request failed',
      );
    } else {
      logger.info(
        { taskId: payload.taskId, status: response.status },
        'Task completion webhook sent',
      );
    }
  } catch (err) {
    logger.warn(
      { err, taskId: payload.taskId },
      'Task completion webhook request error',
    );
  }
}

async function runTask(
  task: ScheduledTask,
  deps: SchedulerDependencies,
): Promise<void> {
  const startTime = Date.now();
  const groupDir = path.join(GROUPS_DIR, task.group_folder);
  fs.mkdirSync(groupDir, { recursive: true });

  logger.info(
    { taskId: task.id, group: task.group_folder },
    'Running scheduled task',
  );

  const groups = deps.registeredGroups();
  const group = Object.values(groups).find(
    (g) => g.folder === task.group_folder,
  );

  if (!group) {
    logger.error(
      { taskId: task.id, groupFolder: task.group_folder },
      'Group not found for task',
    );
    logTaskRun({
      task_id: task.id,
      run_at: new Date().toISOString(),
      duration_ms: Date.now() - startTime,
      status: 'error',
      result: null,
      error: `Group not found: ${task.group_folder}`,
    });
    updateTaskAfterRun(
      task.id,
      null,
      `Error: Group not found: ${task.group_folder}`,
    );
    deps.refreshTaskSnapshots();
    return;
  }

  // Update tasks snapshot for container to read (filtered by group)
  const isMain = task.group_folder === MAIN_GROUP_FOLDER;
  const tasks = getAllTasks();
  writeTasksSnapshot(
    task.group_folder,
    isMain,
    tasks.map((t) => ({
      id: t.id,
      groupFolder: t.group_folder,
      prompt: t.prompt,
      schedule_type: t.schedule_type,
      schedule_value: t.schedule_value,
      status: t.status,
      next_run: t.next_run,
    })),
  );

  let result: string | null = null;
  let error: string | null = null;
  const stopCapture = deps.beginChatOutputCapture(task.chat_jid);

  // For group context mode, use the group's current session
  const sessions = deps.getSessions();
  const sessionId =
    task.context_mode === 'group' ? sessions[task.group_folder] : undefined;

  // Idle timer: writes _close sentinel after IDLE_TIMEOUT of no output,
  // so the container exits instead of hanging at waitForIpcMessage forever.
  let idleTimer: ReturnType<typeof setTimeout> | null = null;

  const resetIdleTimer = () => {
    if (idleTimer) clearTimeout(idleTimer);
    idleTimer = setTimeout(() => {
      logger.debug(
        { taskId: task.id },
        'Scheduled task idle timeout, closing container stdin',
      );
      deps.queue.closeStdin(task.chat_jid);
    }, IDLE_TIMEOUT);
  };

  try {
    const output = await runContainerAgent(
      group,
      {
        prompt: task.prompt,
        sessionId,
        groupFolder: task.group_folder,
        chatJid: task.chat_jid,
        isMain,
        isScheduledTask: true,
      },
      (proc, containerName) =>
        deps.onProcess(task.chat_jid, proc, containerName, task.group_folder),
      async (streamedOutput: ContainerOutput) => {
        if (streamedOutput.result) {
          result = streamedOutput.result;
          // Forward result to user (strip <internal> tags)
          const text = streamedOutput.result
            .replace(/<internal>[\s\S]*?<\/internal>/g, '')
            .trim();
          if (text) {
            await deps.sendMessage(
              task.chat_jid,
              `${deps.assistantName}: ${text}`,
              { dropIfNoListener: true },
            );
          }
          resetIdleTimer();
        } else if (streamedOutput.status === 'success') {
          // End-of-turn marker: agent finished its turn.
          // Scheduled tasks are one-shot — close the container promptly
          // instead of waiting for the full idle timeout.
          if (idleTimer) clearTimeout(idleTimer);
          idleTimer = setTimeout(() => {
            logger.debug(
              { taskId: task.id },
              'Scheduled task turn complete, closing container',
            );
            deps.queue.closeStdin(task.chat_jid);
          }, 3000); // Brief grace period for any final IPC writes
        }
        if (streamedOutput.status === 'error') {
          error = streamedOutput.error || 'Unknown error';
        }
      },
    );

    if (idleTimer) clearTimeout(idleTimer);

    if (output.status === 'error') {
      error = output.error || 'Unknown error';
    } else if (output.result) {
      // Messages are sent via MCP tool (IPC), result text is just logged
      result = output.result;
    }

    logger.info(
      { taskId: task.id, durationMs: Date.now() - startTime },
      'Task completed',
    );
  } catch (err) {
    if (idleTimer) clearTimeout(idleTimer);
    error = err instanceof Error ? err.message : String(err);
    logger.error({ taskId: task.id, error }, 'Task failed');
  }

  const chatMessages = stopCapture();

  // Post-processing: log run, update status, notify webhook.
  // Wrapped in try/catch so a failure here doesn't leave the task stuck at 'running'.
  const durationMs = Date.now() - startTime;
  let nextRun: string | null = null;
  const cleanResult =
    result
      ?.replace(/<think>[\s\S]*?<\/think>/g, '')
      .replace(/<internal>[\s\S]*?<\/internal>/g, '')
      .trim() || null;

  try {
    logTaskRun({
      task_id: task.id,
      run_at: new Date().toISOString(),
      duration_ms: durationMs,
      status: error ? 'error' : 'success',
      result,
      error,
    });
  } catch (err) {
    logger.error({ taskId: task.id, err }, 'Failed to log task run');
  }

  try {
    if (task.schedule_type === 'cron') {
      const interval = CronExpressionParser.parse(task.schedule_value, {
        tz: TIMEZONE,
      });
      nextRun = interval.next().toISOString();
    } else if (task.schedule_type === 'interval') {
      const ms = parseInt(task.schedule_value, 10);
      nextRun = new Date(Date.now() + ms).toISOString();
    }
    // 'once' tasks have no next run

    const resultSummary = error
      ? `Error: ${error}`
      : cleanResult
        ? cleanResult.slice(0, 200)
        : 'Completed';
    updateTaskAfterRun(task.id, nextRun, resultSummary);
    logger.info(
      { taskId: task.id, nextRun, status: nextRun ? 'active' : 'completed' },
      'Task status updated',
    );
  } catch (err) {
    logger.error({ taskId: task.id, err }, 'Failed to update task after run');
  }

  const updatedTask = getTaskById(task.id);
  const finalStatus =
    updatedTask?.status ?? (nextRun === null ? 'completed' : 'active');
  const chatOutput = chatMessages.length > 0 ? chatMessages.join('\n') : null;
  await notifyTaskCompletion({
    taskId: task.id,
    groupFolder: task.group_folder,
    chatJid: task.chat_jid,
    scheduleType: task.schedule_type,
    scheduleValue: task.schedule_value,
    durationMs,
    runAt: new Date().toISOString(),
    nextRun,
    status: finalStatus,
    success: !error,
    resultSummary: error
      ? `Error: ${error}`
      : cleanResult
        ? cleanResult.slice(0, 200)
        : 'Completed',
    chatOutput,
    error,
  });

  // Keep container task snapshots aligned with DB after status transitions.
  deps.refreshTaskSnapshots();
}

let schedulerRunning = false;

export function startSchedulerLoop(deps: SchedulerDependencies): void {
  if (schedulerRunning) {
    logger.debug('Scheduler loop already running, skipping duplicate start');
    return;
  }
  schedulerRunning = true;

  // Recover tasks stuck in 'running' from a previous crash/restart
  const recovered = recoverStuckTasks();
  if (recovered > 0) {
    logger.info({ recovered }, 'Recovered stuck tasks (running → active)');
  }

  logger.info('Scheduler loop started');

  const loop = async () => {
    try {
      const dueTasks = getDueTasks();
      if (dueTasks.length > 0) {
        logger.info({ count: dueTasks.length }, 'Found due tasks');
      }

      for (const task of dueTasks) {
        if (!claimTaskForRun(task.id)) {
          continue;
        }

        const currentTask = getTaskById(task.id);
        if (!currentTask || currentTask.status !== 'running') {
          continue;
        }

        deps.queue.enqueueTask(currentTask.chat_jid, currentTask.id, () =>
          runTask(currentTask, deps),
        );
      }
    } catch (err) {
      logger.error({ err }, 'Error in scheduler loop');
    }

    setTimeout(loop, SCHEDULER_POLL_INTERVAL);
  };

  loop();
}

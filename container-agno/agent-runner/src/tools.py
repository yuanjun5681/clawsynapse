"""NanoClaw IPC tools for the Agno agent.

Replaces the TypeScript MCP server (ipc-mcp-stdio.ts) with native @tool functions.
File formats are kept identical for host compatibility.
"""

import json
import os
import random
import string
from datetime import datetime

from .config import IPC_MESSAGES_DIR, IPC_TASKS_DIR, IPC_DIR


# Module-level context — set by main.py before agent runs
_chat_jid: str = ""
_group_folder: str = ""
_is_main: bool = False


def set_context(chat_jid: str, group_folder: str, is_main: bool) -> None:
    """Set the IPC context for tools. Called once at startup."""
    global _chat_jid, _group_folder, _is_main
    _chat_jid = chat_jid
    _group_folder = group_folder
    _is_main = is_main


def _write_ipc_file(directory: str, data: dict) -> str:
    """Atomic write: temp file then rename. Returns filename."""
    os.makedirs(directory, exist_ok=True)
    try:
        # Host/container UIDs may differ; keep IPC dirs writable by both sides.
        os.chmod(directory, 0o777)
    except OSError:
        pass
    rand = "".join(random.choices(string.ascii_lowercase + string.digits, k=6))
    filename = f"{int(datetime.now().timestamp() * 1000)}-{rand}.json"
    filepath = os.path.join(directory, filename)
    temp_path = f"{filepath}.tmp"
    with open(temp_path, "w") as f:
        json.dump(data, f, indent=2)
    try:
        os.chmod(temp_path, 0o666)
    except OSError:
        pass
    os.rename(temp_path, filepath)
    try:
        os.chmod(filepath, 0o666)
    except OSError:
        pass
    return filename


def send_message(text: str) -> str:
    """Send a message to the user or group immediately while you're still running.

    Use this for progress updates or to send multiple messages.
    You can call this multiple times.
    Note: when running as a scheduled task, your final output is NOT sent
    to the user — use this tool if you need to communicate.

    Args:
        text: The message text to send.

    Returns:
        Confirmation that the message was sent.
    """
    data = {
        "type": "message",
        "chatJid": _chat_jid,
        "text": text,
        "groupFolder": _group_folder,
        "timestamp": datetime.now().isoformat(),
    }
    _write_ipc_file(IPC_MESSAGES_DIR, data)
    return "Message sent."


def schedule_task(
    prompt: str,
    schedule_type: str,
    schedule_value: str,
    context_mode: str = "group",
    target_group_jid: str | None = None,
) -> str:
    """Schedule a recurring or one-time task. The task runs as a full agent with all tools.

    CONTEXT MODE:
    - "group": Task runs with chat history. Use for tasks needing conversation context.
    - "isolated": Task runs in a fresh session. Include all context in the prompt.

    SCHEDULE VALUE FORMAT (all times are LOCAL timezone):
    - cron: e.g. "0 9 * * *" for daily at 9am
    - interval: milliseconds, e.g. "300000" for 5 minutes
    - once: local timestamp like "2026-02-01T15:30:00" (no Z suffix)

    Args:
        prompt: What the agent should do when the task runs.
        schedule_type: One of "cron", "interval", or "once".
        schedule_value: Cron expression, milliseconds, or ISO timestamp.
        context_mode: "group" (with chat history) or "isolated" (fresh session).
        target_group_jid: (Main only) JID of group to schedule for. Defaults to current group.

    Returns:
        Confirmation with task details.
    """
    if schedule_type not in ("cron", "interval", "once"):
        return f'Invalid schedule_type: "{schedule_type}". Must be "cron", "interval", or "once".'

    if schedule_type == "interval":
        try:
            ms = int(schedule_value)
            if ms <= 0:
                raise ValueError()
        except (ValueError, TypeError):
            return f'Invalid interval: "{schedule_value}". Must be positive milliseconds (e.g., "300000" for 5 min).'

    if schedule_type == "once":
        try:
            datetime.fromisoformat(schedule_value)
        except ValueError:
            return f'Invalid timestamp: "{schedule_value}". Use ISO 8601 format like "2026-02-01T15:30:00".'

    if context_mode not in ("group", "isolated"):
        return f'Invalid context_mode: "{context_mode}". Must be "group" or "isolated".'

    target_jid = (target_group_jid if _is_main and target_group_jid else _chat_jid)

    data = {
        "type": "schedule_task",
        "prompt": prompt,
        "schedule_type": schedule_type,
        "schedule_value": schedule_value,
        "context_mode": context_mode,
        "targetJid": target_jid,
        "createdBy": _group_folder,
        "timestamp": datetime.now().isoformat(),
    }
    filename = _write_ipc_file(IPC_TASKS_DIR, data)
    return f"Task scheduled ({filename}): {schedule_type} - {schedule_value}"


def list_tasks() -> str:
    """List all scheduled tasks.

    From main: shows all tasks. From other groups: shows only that group's tasks.

    Returns:
        Formatted list of scheduled tasks.
    """
    tasks_file = os.path.join(IPC_DIR, "current_tasks.json")

    if not os.path.exists(tasks_file):
        return "No scheduled tasks found."

    try:
        with open(tasks_file) as f:
            all_tasks = json.load(f)
    except (json.JSONDecodeError, OSError) as e:
        return f"Error reading tasks: {e}"

    tasks = all_tasks if _is_main else [
        t for t in all_tasks if t.get("groupFolder") == _group_folder
    ]

    if not tasks:
        return "No scheduled tasks found."

    lines = []
    for t in tasks:
        prompt_preview = t.get("prompt", "")[:50]
        lines.append(
            f"- [{t['id']}] {prompt_preview}... "
            f"({t['schedule_type']}: {t['schedule_value']}) - "
            f"{t['status']}, next: {t.get('next_run') or 'N/A'}"
        )
    return f"Scheduled tasks:\n" + "\n".join(lines)


def pause_task(task_id: str) -> str:
    """Pause a scheduled task. It will not run until resumed.

    Args:
        task_id: The task ID to pause.

    Returns:
        Confirmation that the pause was requested.
    """
    data = {
        "type": "pause_task",
        "taskId": task_id,
        "groupFolder": _group_folder,
        "isMain": _is_main,
        "timestamp": datetime.now().isoformat(),
    }
    _write_ipc_file(IPC_TASKS_DIR, data)
    return f"Task {task_id} pause requested."


def resume_task(task_id: str) -> str:
    """Resume a paused task.

    Args:
        task_id: The task ID to resume.

    Returns:
        Confirmation that the resume was requested.
    """
    data = {
        "type": "resume_task",
        "taskId": task_id,
        "groupFolder": _group_folder,
        "isMain": _is_main,
        "timestamp": datetime.now().isoformat(),
    }
    _write_ipc_file(IPC_TASKS_DIR, data)
    return f"Task {task_id} resume requested."


def cancel_task(task_id: str) -> str:
    """Cancel and delete a scheduled task.

    Args:
        task_id: The task ID to cancel.

    Returns:
        Confirmation that the cancellation was requested.
    """
    data = {
        "type": "cancel_task",
        "taskId": task_id,
        "groupFolder": _group_folder,
        "isMain": _is_main,
        "timestamp": datetime.now().isoformat(),
    }
    _write_ipc_file(IPC_TASKS_DIR, data)
    return f"Task {task_id} cancellation requested."


def register_group(jid: str, name: str, folder: str, trigger: str) -> str:
    """Register a new WhatsApp group so the agent can respond there. Main group only.

    Use available_groups.json to find the JID for a group.
    The folder name should be lowercase with hyphens (e.g., "family-chat").

    Args:
        jid: The WhatsApp JID (e.g., "120363336345536173@g.us").
        name: Display name for the group.
        folder: Folder name for group files (lowercase, hyphens).
        trigger: Trigger word (e.g., "@Andy").

    Returns:
        Confirmation that the group was registered.
    """
    if not _is_main:
        return "Only the main group can register new groups."

    data = {
        "type": "register_group",
        "jid": jid,
        "name": name,
        "folder": folder,
        "trigger": trigger,
        "timestamp": datetime.now().isoformat(),
    }
    _write_ipc_file(IPC_TASKS_DIR, data)
    return f'Group "{name}" registered. It will start receiving messages immediately.'


def get_ipc_tools(is_main: bool) -> list:
    """Return the list of IPC tool functions based on privilege level."""
    tools = [
        send_message,
        schedule_task,
        list_tasks,
        pause_task,
        resume_task,
        cancel_task,
    ]
    if is_main:
        tools.append(register_group)
    return tools

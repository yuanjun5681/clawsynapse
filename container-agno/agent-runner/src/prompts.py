"""System prompt construction for the Agno agent."""

import os

from .config import NanoClawConfig, GROUP_CLAUDE_MD, GLOBAL_CLAUDE_MD


def build_system_prompt(config: NanoClawConfig) -> str:
    """Build the system prompt by loading CLAUDE.md files and injecting context."""
    parts: list[str] = []

    # Load group-level CLAUDE.md
    if os.path.exists(GROUP_CLAUDE_MD):
        parts.append(open(GROUP_CLAUDE_MD).read().strip())

    # Non-main groups also get the global CLAUDE.md
    if not config.is_main and os.path.exists(GLOBAL_CLAUDE_MD):
        parts.append(open(GLOBAL_CLAUDE_MD).read().strip())

    # Inject NanoClaw context
    context_lines = [
        "# NanoClaw Agent Context",
        f"- Chat JID: {config.chat_jid}",
        f"- Group Folder: {config.group_folder}",
        f"- Role: {'Main (admin)' if config.is_main else 'Group agent'}",
        "",
        "## Available IPC Tools",
        "- `send_message`: Send a message to the user/group immediately",
        "- `schedule_task`: Schedule a recurring or one-time task",
        "- `list_tasks`: List all scheduled tasks",
        "- `pause_task`: Pause a scheduled task",
        "- `resume_task`: Resume a paused task",
        "- `cancel_task`: Cancel and delete a scheduled task",
    ]
    if config.is_main:
        context_lines.append("- `register_group`: Register a new WhatsApp group (main only)")

    parts.append("\n".join(context_lines))

    return "\n\n---\n\n".join(parts)

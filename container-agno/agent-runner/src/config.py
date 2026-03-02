"""Configuration loading from environment variables and stdin JSON."""

from dataclasses import dataclass, field
import os


@dataclass
class ModelConfig:
    model_id: str
    api_key: str
    base_url: str
    temperature: float = 0.7
    max_tokens: int = 16384


@dataclass
class NanoClawConfig:
    prompt: str
    group_folder: str
    chat_jid: str
    is_main: bool
    session_id: str | None = None
    is_scheduled_task: bool = False


# Path constants (inside container)
IPC_DIR = "/workspace/ipc"
IPC_MESSAGES_DIR = os.path.join(IPC_DIR, "messages")
IPC_TASKS_DIR = os.path.join(IPC_DIR, "tasks")
IPC_INPUT_DIR = os.path.join(IPC_DIR, "input")
IPC_INPUT_CLOSE_SENTINEL = os.path.join(IPC_INPUT_DIR, "_close")

GROUP_DIR = "/workspace/group"
GLOBAL_DIR = "/workspace/global"
GLOBAL_CLAUDE_MD = os.path.join(GLOBAL_DIR, "CLAUDE.md")
GROUP_CLAUDE_MD = os.path.join(GROUP_DIR, "CLAUDE.md")

SESSION_DB_PATH = "/workspace/group/agno-sessions.db"

# Skills directory: synced from host container-agno/skills/ into .claude/skills/
# and mounted at /home/node/.claude/skills/ (see container-runner.ts)
SKILLS_DIR = "/home/node/.claude/skills"

IPC_POLL_MS = 0.5  # 500ms in seconds


def load_model_config() -> ModelConfig:
    """Load model configuration from AGNO_* environment variables."""
    model_id = os.environ.get("AGNO_MODEL_ID")
    api_key = os.environ.get("AGNO_API_KEY")
    base_url = os.environ.get("AGNO_BASE_URL")

    if not model_id:
        raise ValueError("AGNO_MODEL_ID environment variable is required")
    if not api_key:
        raise ValueError("AGNO_API_KEY environment variable is required")
    if not base_url:
        raise ValueError("AGNO_BASE_URL environment variable is required")

    temperature = float(os.environ.get("AGNO_TEMPERATURE", "0.7"))
    max_tokens = int(os.environ.get("AGNO_MAX_TOKENS", "102400"))

    return ModelConfig(
        model_id=model_id,
        api_key=api_key,
        base_url=base_url,
        temperature=temperature,
        max_tokens=max_tokens,
    )


def load_nanoclaw_config(stdin_data: dict) -> NanoClawConfig:
    """Load NanoClaw configuration from stdin JSON."""
    return NanoClawConfig(
        prompt=stdin_data["prompt"],
        group_folder=stdin_data["groupFolder"],
        chat_jid=stdin_data["chatJid"],
        is_main=stdin_data["isMain"],
        session_id=stdin_data.get("sessionId"),
        is_scheduled_task=stdin_data.get("isScheduledTask", False),
    )

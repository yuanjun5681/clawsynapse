let apiBase = 'http://127.0.0.1:3100';
let apiToken: string | null = null;

export interface ApiConfig {
  baseUrl?: string;
  authToken?: string | null;
}

export function configureApi(config: ApiConfig): void {
  if (config.baseUrl) {
    apiBase = config.baseUrl;
  }
  apiToken = config.authToken ?? null;
}

function buildHeaders(extra?: Record<string, string>): Record<string, string> {
  const headers: Record<string, string> = { ...(extra ?? {}) };
  if (apiToken) {
    headers.Authorization = `Bearer ${apiToken}`;
  }
  return headers;
}

export interface ChatEvent {
  type: 'message' | 'error' | 'done';
  data: { text?: string; error?: string; sessionId?: string | null };
}

export async function* streamChat(
  prompt: string,
  groupId: string,
): AsyncGenerator<ChatEvent> {
  const res = await fetch(`${apiBase}/api/chat`, {
    method: 'POST',
    headers: buildHeaders({ 'Content-Type': 'application/json' }),
    body: JSON.stringify({ prompt, groupId }),
  });

  if (!res.ok) {
    const err = await res.json().catch(() => ({}));
    yield {
      type: 'error',
      data: { error: err.error || `HTTP ${res.status}` },
    };
    return;
  }

  const reader = res.body?.getReader();
  if (!reader) {
    yield {
      type: 'error',
      data: { error: 'No response stream from backend' },
    };
    return;
  }

  const decoder = new TextDecoder();
  let buffer = '';

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;

    buffer += decoder.decode(value, { stream: true });

    // Parse SSE format
    const parts = buffer.split('\n\n');
    buffer = parts.pop() || '';

    for (const part of parts) {
      let eventType = 'message';
      let data = '';

      for (const line of part.split('\n')) {
        if (line.startsWith('event: ')) {
          eventType = line.slice(7).trim();
        } else if (line.startsWith('data: ')) {
          data = line.slice(6);
        }
      }

      if (!data) continue;

      try {
        const parsed = JSON.parse(data);
        yield { type: eventType as ChatEvent['type'], data: parsed };
      } catch {
        // skip malformed data
      }
    }
  }

  buffer += decoder.decode();
  const tail = buffer.trim();
  if (!tail) return;

  let tailData = '';
  let tailType = 'message';
  for (const line of tail.split('\n')) {
    if (line.startsWith('event: ')) {
      tailType = line.slice(7).trim();
    } else if (line.startsWith('data: ')) {
      tailData = line.slice(6);
    }
  }

  if (!tailData) return;

  try {
    const parsed = JSON.parse(tailData);
    yield { type: tailType as ChatEvent['type'], data: parsed };
  } catch {
    // ignore malformed tail
  }
}

export async function checkHealth(): Promise<boolean> {
  try {
    const res = await fetch(`${apiBase}/api/health`, {
      headers: buildHeaders(),
      signal: AbortSignal.timeout(2000),
    });
    return res.ok;
  } catch {
    return false;
  }
}
